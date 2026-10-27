package cms

import (
	"github.com/microcosm-cc/bluemonday"
	"regexp"
)

// These inert attributes occur in Ghost's published HTML. They do not enable
// executable elements, arbitrary CSS, events, data URLs, or remote ingestion.
// Unsupported embeds remain in the operator archive, never silently removed.
func contentPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// nofollow is an editorial/SEO decision, not an XSS boundary.
	p.RequireNoFollowOnLinks(false)
	p.AllowNoAttrs().OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^(?:nofollow|noopener|noreferrer|ugc|sponsored)(?: (?:nofollow|noopener|noreferrer|ugc|sponsored))*$`)).OnElements("a")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]+(?: [a-zA-Z0-9_-]+)*$`)).OnElements("figure", "img", "code", "pre")
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[\p{L}\p{N}_%-]{0,255}$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(?:lazy|eager)$`)).OnElements("img")
	imageURL := `(?:https://[^\s,"'<>\\]+|/[^/\s,"'<>\\][^\s,"'<>\\]*)`
	candidate := imageURL + ` [1-9][0-9]*(?:w|x)`
	p.AllowAttrs("srcset").Matching(regexp.MustCompile(`^` + candidate + `(?:,\s*` + candidate + `)*$`)).OnElements("img")
	size := `(?:\((?:min|max)-width: [0-9]+px\) )?[0-9]+(?:px|vw)`
	p.AllowAttrs("sizes").Matching(regexp.MustCompile(`^` + size + `(?:,\s*` + size + `)*$`)).OnElements("img")
	p.AllowAttrs("style").Matching(regexp.MustCompile(`^white-space: pre-wrap;$`)).OnElements("span")

	p.AllowAttrs("style").Matching(regexp.MustCompile(`^text-align: (?:left|center|right);$`)).OnElements("td", "th")
	p.AllowElements("iframe")
	p.AllowAttrs("title").Matching(regexp.MustCompile(`^[^\x00-\x1f\x7f]{0,1000}$`)).OnElements("iframe")
	p.AllowAttrs("src").OnElements("iframe") // exact host/path/query checked by validEmbeds
	p.AllowAttrs("width", "height").Matching(regexp.MustCompile(`^[1-9][0-9]{0,3}$`)).OnElements("iframe")
	p.AllowAttrs("frameborder").Matching(regexp.MustCompile(`^0$`)).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").Matching(regexp.MustCompile(`^(?:|true|allowfullscreen)$`)).OnElements("iframe")
	p.AllowAttrs("referrerpolicy").Matching(regexp.MustCompile(`^(?:no-referrer|strict-origin|strict-origin-when-cross-origin)$`)).OnElements("iframe")
	permission := `(?:accelerometer|autoplay|clipboard-write|encrypted-media|gyroscope|picture-in-picture|web-share|fullscreen)`
	p.AllowAttrs("allow").Matching(regexp.MustCompile(`^` + permission + `(?:; ?` + permission + `)*$`)).OnElements("iframe")
	return p
}
