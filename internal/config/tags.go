package config

import (
	"content/publishing"
	"errors"
	"strings"
)

func LoadPublicTagOrigins(get func(string) string) ([]string, error) {
	flag := get("CONTENT_PUBLISHING_TAGS_ENABLED")
	if flag == "" || flag == "false" {
		return nil, nil
	}
	if flag != "true" {
		return nil, errors.New("CONTENT_PUBLISHING_TAGS_ENABLED must be true or false")
	}
	values := strings.Split(get("CONTENT_PUBLISHING_TAG_ORIGINS"), ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values, publishing.ValidateTagOrigins(values)
}
