// Package media owns provider-independent image intake and publication privacy.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxBytes = 10 << 20
const MaxPixels = 24_000_000

var ErrNotFound = errors.New("image not found")
var ErrInvalid = errors.New("unsupported or invalid image")
var ErrQuota = errors.New("image storage quota exceeded")
var ErrBusy = errors.New("image processing is busy")
var ErrUnavailable = errors.New("image storage unavailable")
var IDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)
var KeyPattern = regexp.MustCompile(`^v1/[0-9a-f]{64}/[0-9a-f]{64}\.(png|jpg)$`)

type Asset struct {
	ID, Publication, Key, SHA256, MIME, Extension, State string
	Size                                                 int64
	Width, Height                                        int
	CreatedAt                                            time.Time
}

type Objects interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// Reserve commits a pending intent before object I/O. Finalize and Cleanup hold
// a row lock while invoking the bounded callback; ready objects are immutable.
type Repository interface {
	MediaReserve(context.Context, Asset) (Asset, error)
	MediaFinalize(context.Context, string, string, func(context.Context, Asset) error) (Asset, error)
	MediaGet(context.Context, string, string, bool) (Asset, error)
	MediaCleanup(context.Context, string, time.Time, int, func(context.Context, Asset) error) (int, error)
}

type Service struct {
	Repo          Repository
	Objects       Objects
	Origin        string
	OriginAliases []string
	namespace     string
	slots         chan struct{}
}

func NormalizeOrigin(origin string) (string, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return "", errors.New("media origin must be HTTPS or loopback HTTP root origin")
	}
	return u.Scheme + "://" + u.Host, nil
}

func New(repo Repository, objects Objects, origin, namespace string, aliases ...string) (*Service, error) {
	normalized, err := NormalizeOrigin(origin)
	if err != nil {
		return nil, err
	}
	if len(aliases) > 8 {
		return nil, errors.New("at most eight media origin aliases")
	}
	trusted := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		a, err := NormalizeOrigin(alias)
		if err != nil {
			return nil, err
		}
		trusted = append(trusted, a)
	}
	if repo == nil || objects == nil || namespace == "" {
		return nil, errors.New("media repository and object storage required")
	}
	return &Service{Repo: repo, Objects: objects, Origin: normalized, OriginAliases: trusted, namespace: namespace, slots: make(chan struct{}, 2)}, nil
}
func (s *Service) URL(a Asset) string {
	return s.Origin + "/content/images/" + a.ID + "/image." + a.Extension
}

// Validate decodes real raster data after checking magic, declared MIME and
// dimensions. Original JPEG/PNG bytes are retained (including metadata). SVG,
// GIF, WebP, ICO, remote ingestion and generic files are deliberately unsupported.
func Validate(data []byte, declared, purpose string) (Asset, error) {
	fail := func() (Asset, error) { return Asset{}, ErrInvalid }
	if len(data) == 0 || len(data) > MaxBytes {
		return fail()
	}
	actual := http.DetectContentType(data)
	if actual != "image/png" && actual != "image/jpeg" {
		return fail()
	}
	if declared != "" {
		m, _, e := mime.ParseMediaType(declared)
		if e != nil || (m != "application/octet-stream" && m != actual) {
			return fail()
		}
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 12000 || cfg.Height > 12000 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return fail()
	}
	if purpose == "" {
		purpose = "image"
	}
	if purpose != "image" && purpose != "profile_image" && purpose != "icon" {
		return fail()
	}
	if purpose != "image" && cfg.Width != cfg.Height {
		return fail()
	}
	if _, decoded, e := image.Decode(bytes.NewReader(data)); e != nil || decoded != format {
		return fail()
	}
	ext := "png"
	if format == "jpeg" {
		ext = "jpg"
	} else if format != "png" {
		return fail()
	}
	sum := sha256.Sum256(data)
	return Asset{SHA256: hex.EncodeToString(sum[:]), MIME: actual, Extension: ext, Size: int64(len(data)), Width: cfg.Width, Height: cfg.Height}, nil
}
func (s *Service) Upload(ctx context.Context, publication string, data []byte, declared, purpose string) (Asset, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Asset{}, ErrBusy
	}
	a, e := Validate(data, declared, purpose)
	if e != nil {
		return Asset{}, e
	}
	if publication == "" || len(publication) > 128 {
		return Asset{}, ErrInvalid
	}
	id := make([]byte, 12)
	if _, e = rand.Read(id); e != nil {
		return Asset{}, ErrUnavailable
	}
	a.ID = hex.EncodeToString(id)
	a.Publication = publication
	scope := sha256.Sum256([]byte(s.namespace + "\x00" + publication))
	a.Key = fmt.Sprintf("v1/%x/%s.%s", scope, a.SHA256, a.Extension)
	a, e = s.Repo.MediaReserve(ctx, a)
	if errors.Is(e, ErrQuota) {
		return Asset{}, ErrQuota
	}
	if e != nil {
		return Asset{}, ErrUnavailable
	}
	a, e = s.Repo.MediaFinalize(ctx, publication, a.ID, func(ctx context.Context, pending Asset) error {
		return s.Objects.Put(ctx, pending.Key, data, pending.MIME)
	})
	if e != nil {
		return Asset{}, ErrUnavailable
	}
	return a, nil
}

// Metadata checks current publication eligibility and returns immutable metadata.
// It deliberately does not contact object storage: success means eligible, not
// that the object is currently reachable. Read still verifies the actual bytes.
func (s *Service) Metadata(ctx context.Context, publication, id string, publicOnly bool) (Asset, error) {
	if !IDPattern.MatchString(id) {
		return Asset{}, ErrNotFound
	}
	a, e := s.Repo.MediaGet(ctx, publication, id, publicOnly)
	if e != nil {
		return Asset{}, e
	}
	checksum, e := hex.DecodeString(a.SHA256)
	validType := a.MIME == "image/png" && a.Extension == "png" || a.MIME == "image/jpeg" && a.Extension == "jpg"
	if a.ID != id || a.Publication != publication || a.State != "ready" || !validType || a.Size < 1 || a.Size > MaxBytes || e != nil || len(checksum) != sha256.Size || hex.EncodeToString(checksum) != a.SHA256 {
		return Asset{}, ErrUnavailable
	}
	return a, nil
}

func (s *Service) Read(ctx context.Context, publication, id string, publicOnly bool) (Asset, []byte, error) {
	a, e := s.Metadata(ctx, publication, id, publicOnly)
	if e != nil {
		return Asset{}, nil, e
	}
	object, e := s.Objects.Get(ctx, a.Key)
	if e != nil {
		return Asset{}, nil, ErrUnavailable
	}
	defer object.Close()
	data, e := io.ReadAll(io.LimitReader(object, MaxBytes+1))
	sum := sha256.Sum256(data)
	if e != nil || int64(len(data)) != a.Size || hex.EncodeToString(sum[:]) != a.SHA256 {
		return Asset{}, nil, ErrUnavailable
	}
	return a, data, nil
}
func (s *Service) Cleanup(ctx context.Context, publication string, before time.Time) (int, error) {
	if publication == "" || before.After(time.Now().Add(-24*time.Hour)) {
		return 0, errors.New("cleanup requires publication and pending age of at least 24 hours")
	}
	return s.Repo.MediaCleanup(ctx, publication, before, 50, func(ctx context.Context, a Asset) error { return s.Objects.Delete(ctx, a.Key) })
}
func ValidObjectKey(key string) bool {
	return KeyPattern.MatchString(key) && !strings.Contains(key, "..")
}
