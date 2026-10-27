package cms

import (
	"strings"
	"testing"
)

func TestPublicAuthorValidation(t *testing.T) {
	good := Author{ID: "0123456789abcdef01234568", Name: strings.Repeat("😀", 255), Slug: "synthetic-author"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []Author{good, good, good, good, good, good}
	cases[0].ID = "not-an-id"
	cases[1].Name = " \t"
	cases[2].Name = strings.Repeat("😀", 256)
	cases[3].Name = "synthetic\x00name"
	cases[4].Slug = "Upper Case"
	cases[5].ID = "0123456789ABCDEF01234568"
	for i, a := range cases {
		if err := a.Validate(); err == nil {
			t.Fatal("invalid public author accepted", i)
		}
	}
}
