package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func raster(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.RGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	var b bytes.Buffer
	var e error
	if format == "png" {
		e = png.Encode(&b, im)
	} else {
		e = jpeg.Encode(&b, im, nil)
	}
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestImageValidation(t *testing.T) {
	p := raster(t, "png", 64, 48)
	j := raster(t, "jpeg", 32, 32)
	for _, tc := range []struct {
		name          string
		data          []byte
		mime, purpose string
		ok            bool
	}{{"png", p, "image/png", "image", true}, {"jpeg", j, "image/jpeg", "profile_image", true}, {"sniff", p, "application/octet-stream", "", true}, {"spoof", p, "image/jpeg", "image", false}, {"svg", []byte(`<svg onload="alert(1)"></svg>`), "image/png", "", false}, {"truncated", p[:40], "image/png", "", false}, {"nonsquare", p, "image/png", "icon", false}, {"purpose", j, "image/jpeg", "anything", false}, {"size", bytes.Repeat([]byte("x"), MaxBytes+1), "image/png", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			a, e := Validate(tc.data, tc.mime, tc.purpose)
			if (e == nil) != tc.ok {
				t.Fatal(a, e)
			}
			if e == nil && (len(a.SHA256) != 64 || a.Size != int64(len(tc.data))) {
				t.Fatal("metadata")
			}
		})
	}
	huge := append([]byte(nil), p...)
	binary.BigEndian.PutUint32(huge[16:20], 12000)
	binary.BigEndian.PutUint32(huge[20:24], 12000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	if _, e := Validate(huge, "image/png", ""); e == nil {
		t.Fatal("pixel limit bypassed")
	}
}
func TestOwnedImageReferences(t *testing.T) {
	origin := "https://media.example.invalid"
	id := "0123456789abcdef01234567"
	body := `<p><img src="` + origin + `/content/images/` + id + `/image.png"></p>`
	ids, e := References(origin, body+body)
	if e != nil || len(ids) != 1 || ids[0].ID != id {
		t.Fatal(ids, e)
	}
	for _, u := range []string{origin + "/content/images/" + id + "/image.png?key=bad", origin + "/content/images/../../escape"} {
		if _, owned, e := ReferenceID(origin, u); !owned || e == nil {
			t.Fatal("unsafe local reference")
		}
	}
	if ids, e = References(origin, strings.ReplaceAll(body, origin, "https://external.invalid")); e != nil || len(ids) != 0 {
		t.Fatal("remote URL treated as owned")
	}
}
