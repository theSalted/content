package httpapi

import (
	"content/cms"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArchivePendingIsConflict(t *testing.T) {
	w := httptest.NewRecorder()
	storeError(w, cms.ErrArchivePending)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "archive delivery is pending") {
		t.Fatal(w.Code, w.Body.String())
	}
}
