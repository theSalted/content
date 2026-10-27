package publishing

import (
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DiscoveryURL is deliberately stricter than arbitrary HTML links. Never fetch
// this URL with a publisher credential; it is public, event-bound evidence only.
func DiscoveryURL(s string) bool {
	if len(s) == 0 || len(s) > 2048 || !utf8.ValidString(s) || strings.ContainsAny(s, "?#\\") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()) {
		return false
	}
	for _, r := range u.Path {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
