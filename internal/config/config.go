package config

import (
	"content/cms"
	"content/internal/review"
	"content/publishing"
	"errors"
	"strings"
)

type Config struct {
	PublishingIdentity                                                                    bool
	Scheduler                                                                             bool
	FederationDestination                                                                 string
	PublicTagOrigins                                                                      []string
	Media                                                                                 *Media
	Review                                                                                *review.Config
	DefaultAuthor                                                                         *cms.Author
	DatabaseURL, Schema, Installation, Publication, SiteURL, AdminKey, ContentKey, Listen string
	PageURL                                                                               string
	SiteTitle, SiteDescription, SiteLogo                                                  string
	Enrollment                                                                            *publishing.Enrollment
}

func Load(command string, get func(string) string) (Config, error) {
	c := Config{Schema: get("CONTENT_SCHEMA"), Installation: get("CONTENT_INSTALLATION_ID")}
	if c.Schema == "" || c.Installation == "" {
		return c, errors.New("CONTENT_SCHEMA and CONTENT_INSTALLATION_ID are required")
	}
	if command == "migrate" {
		c.DatabaseURL = get("CONTENT_MIGRATION_DATABASE_URL")
		if c.DatabaseURL == "" {
			return c, errors.New("CONTENT_MIGRATION_DATABASE_URL is required; runtime credentials are never used as a fallback")
		}
		return c, nil
	}
	if command != "serve" {
		return c, errors.New("expected serve or migrate")
	}
	flagScheduler := get("CONTENT_SCHEDULER_ENABLED")
	if flagScheduler != "" && flagScheduler != "false" && flagScheduler != "true" {
		return c, errors.New("CONTENT_SCHEDULER_ENABLED must be true or false")
	}
	c.Scheduler = flagScheduler == "true"
	flagRedirect := get("CONTENT_SLUG_REDIRECTS_ENABLED")
	if flagRedirect != "" && flagRedirect != "false" && flagRedirect != "true" {
		return c, errors.New("CONTENT_SLUG_REDIRECTS_ENABLED must be true or false")
	}
	var mediaErr error
	c.Media, mediaErr = LoadMedia(get)
	if mediaErr != nil {
		return c, mediaErr
	}
	c.DatabaseURL = get("CONTENT_DATABASE_URL")
	c.Publication = get("CONTENT_PUBLICATION_ID")
	c.SiteURL = get("CONTENT_SITE_URL")
	c.PageURL = get("CONTENT_PAGE_URL")
	author := cms.Author{ID: get("CONTENT_AUTHOR_ID"), Name: get("CONTENT_AUTHOR_NAME"), Slug: get("CONTENT_AUTHOR_SLUG")}
	if author.ID != "" || author.Name != "" || author.Slug != "" {
		if err := author.Validate(); err != nil {
			return c, err
		}
		c.DefaultAuthor = &author
	}
	identityFlag := get("CONTENT_PUBLISHING_IDENTITY_ENABLED")
	if identityFlag != "" && identityFlag != "false" && identityFlag != "true" {
		return c, errors.New("CONTENT_PUBLISHING_IDENTITY_ENABLED must be true or false")
	}
	c.PublishingIdentity = identityFlag == "true"
	if c.PublishingIdentity && c.DefaultAuthor == nil {
		return c, errors.New("publishing identity requires explicit public author configuration")
	}
	reviewOrigin, reviewUser, reviewHash := get("CONTENT_REVIEW_ORIGIN"), get("CONTENT_REVIEW_USERNAME"), get("CONTENT_REVIEW_PASSWORD_HASH")
	if reviewOrigin != "" || reviewUser != "" || reviewHash != "" {
		if reviewOrigin == "" || reviewUser == "" || reviewHash == "" {
			return c, errors.New("review requires CONTENT_REVIEW_ORIGIN, CONTENT_REVIEW_USERNAME and CONTENT_REVIEW_PASSWORD_HASH together")
		}
		c.Review = &review.Config{Origin: reviewOrigin, Publication: c.Publication, Username: reviewUser, PasswordHash: reviewHash, Scheduling: c.Scheduler, SlugRedirects: flagRedirect == "true"}
		if raw := get("CONTENT_REVIEW_ORIGIN_ALIASES"); raw != "" {
			for _, alias := range strings.Split(raw, ",") {
				c.Review.OriginAliases = append(c.Review.OriginAliases, strings.TrimSpace(alias))
			}
		}
	}
	var federationErr error
	c.FederationDestination, federationErr = LoadFederationProjection(get)
	if federationErr != nil {
		return c, federationErr
	}
	var tagErr error
	c.PublicTagOrigins, tagErr = LoadPublicTagOrigins(get)
	if tagErr != nil {
		return c, tagErr
	}
	c.SiteTitle = get("CONTENT_SITE_TITLE")
	c.SiteDescription = get("CONTENT_SITE_DESCRIPTION")
	c.SiteLogo = get("CONTENT_SITE_LOGO")
	c.AdminKey = get("CONTENT_ADMIN_KEY")
	c.ContentKey = get("CONTENT_CONTENT_KEY")
	c.Listen = get("CONTENT_LISTEN")
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.DatabaseURL == "" || c.Publication == "" || c.SiteURL == "" || c.AdminKey == "" || strings.TrimSpace(c.ContentKey) == "" {
		return c, errors.New("runtime database URL, publication ID, site URL and caller-supplied admin/content keys are required")
	}
	flag := get("CONTENT_PUBLISHING_ENROLL_NEW")
	if flag != "" && flag != "false" && flag != "true" {
		return c, errors.New("CONTENT_PUBLISHING_ENROLL_NEW must be true or false")
	}
	if flag == "true" {
		enrollment := publishing.Enrollment{Destination: get("CONTENT_PUBLISHING_DESTINATION"), SourceID: get("CONTENT_PUBLISHING_SOURCE"), SiteURL: c.SiteURL}
		if err := enrollment.Validate(); err != nil {
			return c, err
		}
		c.Enrollment = &enrollment
	}
	return c, nil
}
