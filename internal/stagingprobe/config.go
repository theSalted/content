// Package stagingprobe is an explicit synthetic verification tool, never imported
// by the CMS server. It performs no migrations, role creation or identity setup.
package stagingprobe

import (
	"content/publishing"
	"content/storage/postgres"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	APIURL, DeliveryURL, Schema, Installation, Publication, Source, Destination string
	Endpoint, Token, SiteURL, PeerURL, RunID, ActorURL                          string
	RequirePeer                                                                 bool
}

func Load(get func(string) string) (Config, func(), error) {
	fail := func() (Config, func(), error) {
		return Config{}, func() {}, errors.New("invalid_staging_configuration")
	}
	if get("CONTENT_PUBLISHING_ENROLL_NEW") != "true" || get("CONTENT_PUBLISHING_DELIVERY_ENABLED") != "true" {
		return fail()
	}
	c := Config{APIURL: get("CONTENT_DATABASE_URL"), DeliveryURL: get("CONTENT_DELIVERY_DATABASE_URL"), Schema: get("CONTENT_SCHEMA"), Installation: get("CONTENT_INSTALLATION_ID"), Publication: get("CONTENT_PUBLICATION_ID"), Source: get("CONTENT_PUBLISHING_SOURCE"), Destination: get("CONTENT_PUBLISHING_DESTINATION"), Endpoint: get("CONTENT_PUBLISHING_ENDPOINT"), Token: get("CONTENT_PUBLISHING_TOKEN"), SiteURL: get("CONTENT_SITE_URL"), PeerURL: get("CONTENT_STAGING_PEER_STATUS_URL"), RunID: get("CONTENT_STAGING_RUN_ID"), RequirePeer: true}
	if !publishing.ValidUUID(c.RunID) || c.Publication != "content-staging" || c.Source != "content" || c.Destination != "activitypub" || !strings.Contains(c.Schema, "staging") || !strings.Contains(c.Installation, "staging") {
		return fail()
	}
	if _, e := postgres.Schema(c.Schema); e != nil {
		return fail()
	}
	endpoint, e := url.Parse(c.Endpoint)
	if e != nil {
		return fail()
	}
	peer, e := url.Parse(c.PeerURL)
	if e != nil || peer.Scheme != endpoint.Scheme || peer.Host != endpoint.Host || peer.Path != "/internal/staging/peer/status" || peer.User != nil || peer.RawQuery != "" || peer.Fragment != "" {
		return fail()
	}
	// Cloud fixture must use the co-located private listener, never public ingress.
	if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" {
		return fail()
	}
	sender, e := publishing.NewHTTPClient(c.Endpoint, c.Token)
	if e != nil {
		return fail()
	}
	sender.Close()
	site, e := url.Parse(c.SiteURL)
	if e != nil || !publishing.PublicURL(c.SiteURL) || (site.Path != "" && site.Path != "/") {
		return fail()
	}
	urls := []*url.URL{}
	for _, raw := range []string{c.APIURL, c.DeliveryURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" || u.User == nil || u.User.Username() == "" {
			return fail()
		}
		urls = append(urls, u)
	}
	if urls[0].User.Username() == urls[1].User.Username() || urls[0].Hostname() != urls[1].Hostname() || urls[0].Path != urls[1].Path {
		return fail()
	}
	ca := get("CONTENT_STAGING_DATABASE_SSL_CA")
	cleanup := func() {}
	caPath := ""
	if ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			return fail()
		}
		f, e := os.CreateTemp("", "content-staging-ca-*.pem")
		if e != nil {
			return fail()
		}
		caPath = f.Name()
		cleanup = func() { _ = os.Remove(caPath) }
		if _, e = f.WriteString(ca); e != nil {
			f.Close()
			cleanup()
			return fail()
		}
		if f.Close() != nil {
			cleanup()
			return fail()
		}
	}
	// The cloud runner never permits unverified TLS, plaintext or an insecure
	// fallback. Local integration tests construct Config directly in test code.
	for i, u := range urls {
		q := u.Query()
		if q.Get("sslmode") != "verify-full" {
			cleanup()
			return fail()
		}
		if caPath != "" {
			q.Set("sslrootcert", caPath)
		}
		u.RawQuery = q.Encode()
		if i == 0 {
			c.APIURL = u.String()
		} else {
			c.DeliveryURL = u.String()
		}
	}
	return c, cleanup, nil
}
