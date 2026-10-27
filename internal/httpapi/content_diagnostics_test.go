package httpapi

import (
	"bytes"
	"content/cms"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestContentDiagnosticInventoryNeverContainsValues(t *testing.T) {
	var b bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, nil)))
	defer slog.SetDefault(old)
	in := cms.CreatePost{Title: "PRIVATE_TITLE", HTML: `<p class="PRIVATE_CLASS"><a href="file:///PRIVATE_PATH" target="_blank">PRIVATE_TEXT</a><PRIVATE_TAG PRIVATE_ATTR="PRIVATE_VALUE">PRIVATE_TEXT</PRIVATE_TAG><script>PRIVATE_SCRIPT</script></p>`}
	logContentRejection(in)
	if strings.Contains(b.String(), "PRIVATE_") || strings.Contains(b.String(), "file:///") {
		t.Fatal("content leaked")
	}
	var row struct {
		Problems []string            `json:"problems"`
		Shape    map[string][]string `json:"html_shape"`
	}
	if json.Unmarshal(b.Bytes(), &row) != nil || len(row.Problems) != 1 || row.Problems[0] != "html_profile" {
		t.Fatal("reason missing")
	}
	if strings.Join(row.Shape["url_schemes"], ",") != "file" {
		t.Fatal("scheme missing")
	}
	for _, expected := range []string{"a.href", "a.target", "p.class", "other.other"} {
		found := false
		for _, attr := range row.Shape["attributes"] {
			found = found || attr == expected
		}
		if !found {
			t.Fatal("attribute missing", expected)
		}
	}
	b.Reset()
	logValidationReason("PRIVATE_CUSTOM_ERROR")
	if b.Len() != 0 {
		t.Fatal("arbitrary error retained")
	}
}
func TestAllContentValidationCodesReportedTogether(t *testing.T) {
	codes := cms.CreateValidationCodes(cms.CreatePost{Title: " ", Slug: "Invalid Slug", HTML: `<script>no</script>`, Status: "scheduled", Visibility: "members"})
	if strings.Join(codes, ",") != "title,html_profile,status,visibility,slug" {
		t.Fatal(codes)
	}
	if codes = cms.CreateValidationCodes(cms.CreatePost{Title: "Plain", HTML: "<p>Plain.</p>"}); len(codes) != 0 {
		t.Fatal("defaults rejected", codes)
	}
}
func TestUnsafeNativeHTMLRemainsRejected(t *testing.T) {
	s, m, auth := setup(t)
	for _, body := range []string{`<p><a href="file:///private/test">Test</a></p>`, `<script>test</script>`, `<p><a href="javascript:test">Test</a></p>`} {
		raw, _ := json.Marshal(map[string]any{"posts": []any{map[string]any{"title": "Synthetic", "html": body, "featured": false, "published_at": nil, "tags": []any{}}}})
		if w := request(s, "POST", "/ghost/api/admin/posts/?source=html", string(raw), auth); w.Code != 422 || len(m.posts) != 0 {
			t.Fatal("unsafe content saved", w.Code)
		}
	}
}
