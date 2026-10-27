package config

import (
	"errors"
	"regexp"
)

var destinationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Opt-in only after the projection migration, exact role grants and versioned
// receiver acknowledgements are ready. No credential fallback or network I/O.
func LoadFederationProjection(get func(string) string) (string, error) {
	switch get("CONTENT_FEDERATION_PROJECTION_ENABLED") {
	case "", "false":
		return "", nil
	case "true":
		d := get("CONTENT_PUBLISHING_DESTINATION")
		if !destinationPattern.MatchString(d) {
			return "", errors.New("federation projection requires a valid explicit publishing destination")
		}
		return d, nil
	default:
		return "", errors.New("CONTENT_FEDERATION_PROJECTION_ENABLED must be true or false")
	}
}
