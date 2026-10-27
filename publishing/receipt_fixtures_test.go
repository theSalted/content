package publishing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPinnedReceiverReceiptFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/receipt-v1-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "bc9cd446a1ee0fcfcf687abf3c969b97c8f6d3245763a5e44441462fd27b166a" {
		t.Fatal("AP fixture pin changed")
	}
	var all struct {
		Profile  string `json:"profile"`
		Fixtures []struct {
			Name  string `json:"name"`
			Query struct {
				SchemaVersion int             `json:"schema_version"`
				Event         json.RawMessage `json:"event"`
			} `json:"query"`
			Response json.RawMessage `json:"response"`
		} `json:"fixtures"`
	}
	if json.Unmarshal(raw, &all) != nil || all.Profile != "content-activitypub-receipt-resolver-v1" || len(all.Fixtures) != 7 {
		t.Fatal("invalid receipt corpus")
	}
	for _, f := range all.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			if f.Query.SchemaVersion != 1 {
				t.Fatal("query version")
			}
			if _, err := DecodeReceipt(f.Query.Event, f.Response); err != nil {
				t.Fatal("Go rejected AP fixture", err)
			}
		})
	}
}
