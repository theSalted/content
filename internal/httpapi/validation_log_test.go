package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRejectedInputDiagnosticsNeverLogValues(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	s, m, auth := setup(t)
	raw := `{"posts":[{"title":"PRIVATE-TITLE","html":"<p>PRIVATE-BODY</p>","authors":[{"id":"PRIVATE-ID","email":"PRIVATE-EMAIL"}],"feature_image":"https://private.invalid/secret","custom_excerpt":null,"tags":[],"PRIVATE-KEY-NAME":"PRIVATE-KEY-VALUE"}]}`
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", raw, auth)
	if w.Code != 400 || len(m.posts) != 0 {
		t.Fatal("invalid request changed storage")
	}
	log := output.String()
	for _, value := range []string{"PRIVATE-", "private.invalid", auth, fixtureKey} {
		if strings.Contains(log, value) {
			t.Fatal("private input leaked")
		}
	}
	var row struct {
		Reason string            `json:"reason"`
		Shapes map[string]string `json:"post_field_shapes"`
	}
	if json.Unmarshal(output.Bytes(), &row) != nil || row.Reason != "invalid_json_or_unsupported_field" || row.Shapes["authors"] != "array" || row.Shapes["feature_image"] != "string" || row.Shapes["custom_excerpt"] != "null" || row.Shapes["tags"] != "empty_array" {
		t.Fatal("safe shapes missing", log)
	}
	output.Reset()
	logBodyRejection([]byte(`{"PRIVATE-KEY":"PRIVATE-VALUE"}`), "invalid_json_or_unsupported_field")
	if strings.Contains(output.String(), "PRIVATE-") {
		t.Fatal("arbitrary envelope leaked")
	}
}
func TestJSONDiagnosticShapes(t *testing.T) {
	for raw, want := range map[string]string{`null`: "null", `""`: "empty_string", `"secret"`: "string", `true`: "boolean", `4`: "number", `[]`: "empty_array", `["secret"]`: "array", `{}`: "empty_object", `{"secret":"secret"}`: "object", `{`: "invalid"} {
		if got := jsonShape(json.RawMessage(raw)); got != want {
			t.Fatal(raw, got, want)
		}
	}
}

func TestRejectionReasonsAreServerConstantsOnly(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	logRequestRejection("PRIVATE-CREDENTIAL")
	if output.Len() != 0 {
		t.Fatal("arbitrary message logged")
	}
	logRequestRejection("Creation requires source=html; only HTML is implemented")
	if !strings.Contains(output.String(), `"reason":"create_source_or_formats"`) {
		t.Fatal("reason missing")
	}
}
