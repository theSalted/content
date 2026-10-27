package config

import (
	"content/media"
	"content/storage/objects"
	"errors"
	"strings"
)

type Media struct {
	Backend, Origin, Directory string
	OriginAliases              []string
	S3                         objects.S3Config
}

func LoadMedia(get func(string) string) (*Media, error) {
	backend := get("CONTENT_MEDIA_BACKEND")
	if backend == "" {
		return nil, nil
	}
	c := &Media{Backend: backend, Origin: get("CONTENT_MEDIA_ORIGIN")}
	if c.Origin == "" {
		return nil, errors.New("CONTENT_MEDIA_ORIGIN is required when media is enabled")
	}
	var err error
	c.Origin, err = media.NormalizeOrigin(c.Origin)
	if err != nil {
		return nil, err
	}
	if raw := get("CONTENT_MEDIA_ORIGIN_ALIASES"); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			a, err := media.NormalizeOrigin(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			c.OriginAliases = append(c.OriginAliases, a)
		}
		if len(c.OriginAliases) > 8 {
			return nil, errors.New("at most eight media origin aliases")
		}
	}
	switch backend {
	case "filesystem":
		c.Directory = get("CONTENT_MEDIA_DIRECTORY")
		if c.Directory == "" {
			return nil, errors.New("CONTENT_MEDIA_DIRECTORY is required")
		}
	case "s3":
		c.S3 = objects.S3Config{Endpoint: get("CONTENT_MEDIA_S3_ENDPOINT"), Region: get("CONTENT_MEDIA_S3_REGION"), Bucket: get("CONTENT_MEDIA_S3_BUCKET"), AccessKey: get("CONTENT_MEDIA_S3_ACCESS_KEY"), SecretKey: get("CONTENT_MEDIA_S3_SECRET_KEY"), SessionToken: get("CONTENT_MEDIA_S3_SESSION_TOKEN"), URLStyle: get("CONTENT_MEDIA_S3_URL_STYLE")}
	default:
		return nil, errors.New("CONTENT_MEDIA_BACKEND must be filesystem or s3")
	}
	return c, nil
}
func LoadMediaCleanup(get func(string) string) (Config, error) {
	c := Config{DatabaseURL: get("CONTENT_MEDIA_CLEANUP_DATABASE_URL"), Schema: get("CONTENT_SCHEMA"), Installation: get("CONTENT_INSTALLATION_ID"), Publication: get("CONTENT_PUBLICATION_ID")}
	if c.DatabaseURL == "" || c.Schema == "" || c.Installation == "" || c.Publication == "" {
		return c, errors.New("media cleanup requires its own explicit database URL, schema, installation and publication")
	}
	var err error
	c.Media, err = LoadMedia(get)
	if err != nil {
		return c, err
	}
	if c.Media == nil {
		return c, errors.New("media cleanup requires configured object storage")
	}
	return c, nil
}
