// Package clientprobe records a bounded, redacted local publishing handshake.
// It has no database, credential generation or application-account integration.
package clientprobe

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxBody = 2 << 20

func LoopbackAddress(address string) bool {
	addr, err := netip.ParseAddrPort(address)
	return err == nil && addr.Addr().IsLoopback() && addr.Port() != 0
}

type Event struct {
	Sequence             uint64            `json:"sequence"`
	Method               string            `json:"method"`
	Route                string            `json:"route"`
	Version              string            `json:"accept_version,omitempty"`
	Query                map[string]string `json:"query,omitempty"`
	JWTShape             map[string]string `json:"unverified_jwt_shape,omitempty"`
	AuthorizationPresent bool              `json:"authorization_present"`
	BodyKind             string            `json:"body_kind"`
	PostFields           map[string]string `json:"post_fields,omitempty"`
	Status               int               `json:"status"`
}

type Recorder struct {
	proxy    *httputil.ReverseProxy
	mu       sync.Mutex
	output   io.Writer
	sequence atomic.Uint64
	failed   atomic.Bool
}

// New only targets a literal loopback HTTP origin. No DNS, environment proxy,
// redirects, TLS trust changes, raw request logs or credential storage.
func New(target string, output io.Writer) (*Recorder, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || !LoopbackAddress(u.Host) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || output == nil {
		return nil, errors.New("probe target must be an HTTP literal-loopback origin with an explicit port")
	}
	r := &Recorder{output: output}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second}
	r.proxy = &httputil.ReverseProxy{
		Rewrite:   func(p *httputil.ProxyRequest) { p.SetURL(u); p.Out.Host = u.Host },
		Transport: transport,
		ErrorLog:  log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "local probe upstream unavailable", 502)
		},
		ModifyResponse: func(res *http.Response) error {
			if location := res.Header.Get("Location"); location != "" {
				v, e := url.Parse(location)
				if e != nil || v.User != nil || (v.IsAbs() && (v.Scheme != "http" || !LoopbackAddress(v.Host))) || (!v.IsAbs() && v.Host != "") {
					return errors.New("nonlocal redirect blocked")
				}
			}
			return nil
		},
	}
	return r, nil
}

func (r *Recorder) Close()       { r.proxy.Transport.(*http.Transport).CloseIdleConnections() }
func (r *Recorder) Failed() bool { return r.failed.Load() }

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}

func (r *Recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.Failed() {
		http.Error(w, "probe recording failed; stop the test", 503)
		return
	}
	e := Event{Sequence: r.sequence.Add(1), Method: safeMethod(req.Method), Route: safeRoute(req.URL.Path), Version: safeVersion(req.Header.Get("Accept-Version")), Query: safeQuery(req.URL.Query()), AuthorizationPresent: req.Header.Get("Authorization") != "", JWTShape: jwtShape(req.Header.Get("Authorization")), BodyKind: "empty"}
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		if sw.status == 0 {
			sw.status = 200
		}
		e.Status = sw.status
		r.mu.Lock()
		defer r.mu.Unlock()
		if json.NewEncoder(r.output).Encode(e) != nil {
			r.failed.Store(true)
		}
	}()
	if e.Method == "[other]" || req.Header.Get("Upgrade") != "" {
		http.Error(sw, "unsupported probe request", 400)
		return
	}
	if req.Body != nil {
		body, err := io.ReadAll(io.LimitReader(req.Body, MaxBody+1))
		req.Body.Close()
		if err != nil {
			http.Error(sw, "cannot read probe request", 400)
			return
		}
		if len(body) > MaxBody {
			e.BodyKind = "too_large"
			http.Error(sw, "probe body limit exceeded", 413)
			return
		}
		e.BodyKind, e.PostFields = bodyShape(body, req.Header.Get("Content-Type"))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.proxy.ServeHTTP(sw, req)
}

var versionPattern = regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,3}$`)
var apiPath = regexp.MustCompile(`^/ghost/api/(admin|content|v[0-9]{1,3}/admin|v[0-9]{1,3}/content)/`)

func safeVersion(s string) string {
	if s == "" || versionPattern.MatchString(s) {
		return s
	}
	return "[other]"
}
func safeMethod(s string) string {
	switch s {
	case "GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH":
		return s
	}
	return "[other]"
}
func safeRoute(path string) string {
	if path == "/ghost/" {
		return path
	}
	prefix := apiPath.FindString(path)
	if prefix == "" {
		return "[unrecognized]"
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	allowed := map[string]bool{"site": true, "posts": true, "pages": true, "users": true, "roles": true, "tags": true, "settings": true, "config": true, "images": true, "session": true}
	if len(parts) == 0 || !allowed[parts[0]] {
		return prefix + "[unrecognized]"
	}
	resource := parts[0]
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		if i == 1 && ((resource == "users" && parts[i] == "me") || (resource == "images" && parts[i] == "upload") || (parts[i] == "slug" || parts[i] == "email")) {
			continue
		}
		parts[i] = "{value}"
	}
	return prefix + strings.Join(parts, "/")
}
func safeQuery(q url.Values) map[string]string {
	out := map[string]string{}
	for key, values := range q {
		switch key {
		case "source", "formats", "include", "fields", "limit", "page", "filter", "key", "save_revision":
		default:
			out["[other]"] = "[redacted]"
			continue
		}
		out[key] = "[redacted]"
		if len(values) != 1 {
			continue
		}
		v := values[0]
		if (key == "source" && v == "html") || (key == "formats" && (v == "html" || v == "lexical" || v == "mobiledoc" || v == "html,lexical")) || (key == "save_revision" && (v == "true" || v == "false")) {
			out[key] = v
		}
	}
	return out
}
func bodyShape(body []byte, contentType string) (string, map[string]string) {
	if len(body) == 0 {
		return "empty", nil
	}
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		return "multipart", nil
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return "other", nil
	}
	var envelope struct {
		Posts []map[string]json.RawMessage `json:"posts"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return "invalid_json", nil
	}
	fields := map[string]string{}
	allowed := map[string]bool{"title": true, "html": true, "lexical": true, "mobiledoc": true, "slug": true, "status": true, "visibility": true, "updated_at": true, "tags": true, "authors": true, "custom_excerpt": true, "feature_image": true}
	for _, post := range envelope.Posts {
		for key, value := range post {
			if !allowed[key] {
				fields["[other]"] = "present"
				continue
			}
			kind := "scalar"
			v := bytes.TrimSpace(value)
			if len(v) > 0 {
				switch v[0] {
				case '"':
					kind = "string"
				case '[':
					kind = "array"
				case '{':
					kind = "object"
				case 'n':
					kind = "null"
				}
			}
			fields[key] = kind
		}
	}
	return "json", fields
}

// Only enumerated non-secret protocol attributes; never the token, kid, signature
// or raw timestamps. This is observation, not authentication or trusted claims.
func jwtShape(header string) map[string]string {
	if header == "" {
		return nil
	}
	out := map[string]string{}
	pieces := strings.SplitN(header, " ", 2)
	if len(pieces) != 2 {
		return map[string]string{"encoding": "other"}
	}
	if strings.EqualFold(pieces[0], "Ghost") {
		out["scheme"] = "Ghost"
	} else {
		out["scheme"] = "other"
	}
	parts := strings.Split(pieces[1], ".")
	if len(parts) != 3 {
		return out
	}
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return out
	}
	var head map[string]any
	if json.Unmarshal(h, &head) != nil {
		return out
	}
	for _, key := range []string{"alg", "typ"} {
		value, _ := head[key].(string)
		if value == "HS256" || value == "JWT" {
			out[key] = value
		} else if value == "" {
			out[key] = "absent"
		} else {
			out[key] = "other"
		}
	}
	kid, _ := head["kid"].(string)
	out["kid_24_hex"] = strconv.FormatBool(regexp.MustCompile(`^[0-9a-fA-F]{24}$`).MatchString(kid))
	c, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out
	}
	var claims map[string]any
	if json.Unmarshal(c, &claims) != nil {
		return out
	}
	aud, _ := claims["aud"].(string)
	switch aud {
	case "/admin/", "/admin", "admin/", "admin", "/v2/admin/", "/v2/admin", "v2/admin/", "v2/admin":
		out["aud"] = aud
	default:
		out["aud"] = "other"
	}
	iat, iok := claims["iat"].(float64)
	exp, eok := claims["exp"].(float64)
	out["iat_present"] = strconv.FormatBool(iok)
	out["exp_present"] = strconv.FormatBool(eok)
	if eok {
		out["expired_at_probe"] = strconv.FormatBool(exp <= float64(time.Now().Unix()))
	}
	if iok {
		ahead := iat - float64(time.Now().UnixNano())/1e9
		out["issued_in_future_at_probe"] = strconv.FormatBool(ahead > 0)
		switch {
		case ahead <= 0:
			out["iat_ahead_bucket"] = "not_future"
		case ahead <= 1:
			out["iat_ahead_bucket"] = "within_one_second"
		case ahead <= 5:
			out["iat_ahead_bucket"] = "one_to_five_seconds"
		default:
			out["iat_ahead_bucket"] = "over_five_seconds"
		}
	}
	if iok && eok && exp-iat >= -3600 && exp-iat <= 3600 {
		out["lifetime_seconds"] = strconv.FormatFloat(exp-iat, 'f', 0, 64)
	}
	return out
}
