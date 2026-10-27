package httpapi

import (
	"content/media"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func WithMedia(service *media.Service) Option {
	return func(s *Server) error {
		if service == nil {
			return errors.New("media service required")
		}
		s.media = service
		return nil
	}
}
func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request, legacyVersion string) {
	if s.media == nil {
		failure(w, 404, "NotFoundError", "Image storage is not configured")
		return
	}
	if !profileHeaders(w, r, legacyVersion) {
		return
	}
	if r.Method != "POST" {
		method(w, "POST")
		return
	}
	audience := "/admin/"
	if legacyVersion != "" {
		audience = "/" + legacyVersion + "/admin/"
	}
	if s.admin.validateAudience(r.Header.Get("Authorization"), time.Now(), audience) != nil {
		failure(w, 401, "UnauthorizedError", "Invalid admin token")
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "BadRequestError", "Image upload query parameters are unsupported")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxBytes+(64<<10))
	reader, e := r.MultipartReader()
	if e != nil {
		failure(w, 400, "BadRequestError", "Expected multipart image upload")
		return
	}
	var data []byte
	var declared, ref, purpose string
	seen := map[string]bool{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			failure(w, 413, "RequestEntityTooLargeError", "Invalid or oversized upload")
			return
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "ref" && name != "purpose") {
			part.Close()
			failure(w, 400, "BadRequestError", "Unsupported or duplicate upload field")
			return
		}
		seen[name] = true
		limit := int64(2048)
		if name == "file" {
			limit = media.MaxBytes
			declared = part.Header.Get("Content-Type")
		}
		value, e := io.ReadAll(io.LimitReader(part, limit+1))
		part.Close()
		if e != nil || int64(len(value)) > limit {
			failure(w, 413, "RequestEntityTooLargeError", "Upload field is too large")
			return
		}
		switch name {
		case "file":
			data = value
		case "ref":
			ref = string(value)
		case "purpose":
			purpose = string(value)
		}
	}
	if !seen["file"] || !utf8.ValidString(ref) || strings.ContainsRune(ref, 0) {
		failure(w, 422, "ValidationError", "Image file and valid reference required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	a, e := s.media.Upload(ctx, s.publication, data, declared, purpose)
	if errors.Is(e, media.ErrInvalid) {
		failure(w, 422, "ValidationError", "Only valid PNG/JPEG images up to 10 MiB and 24 megapixels are supported; profile images/icons must be square")
		return
	}
	if errors.Is(e, media.ErrQuota) {
		failure(w, 507, "InsufficientStorageError", "Image storage quota is full; no bytes were uploaded")
		return
	}
	if errors.Is(e, media.ErrBusy) {
		failure(w, 429, "TooManyRequestsError", "Image processing is busy; retry later")
		return
	}
	if e != nil {
		failure(w, 503, "ServiceUnavailableError", "Image could not be stored; retrying the same bytes is safe")
		return
	}
	w.Header().Set("X-Content-Media-Profile", "images-png-jpeg-v1")
	item := map[string]any{"url": s.media.URL(a)}
	if seen["ref"] {
		item["ref"] = ref
	}
	reply(w, 201, map[string]any{"images": []any{item}})
}
func (s *Server) readImage(w http.ResponseWriter, r *http.Request) {
	if s.media == nil {
		failure(w, 404, "NotFoundError", "Image not found")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		method(w, "GET, HEAD")
		return
	}
	id, ext, ok := media.ImagePath(r.URL.Path)
	if !ok || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		failure(w, 404, "NotFoundError", "Image not found")
		return
	}
	publicOnly := true
	if value := r.Header.Get("Authorization"); value != "" {
		now := time.Now()
		if s.admin.validate(value, now) != nil && s.admin.validateAudience(value, now, "/v2/admin/") != nil && s.admin.validateAudience(value, now, "/v4/admin/") != nil {
			failure(w, 401, "UnauthorizedError", "Invalid admin token")
			return
		}
		publicOnly = false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var a media.Asset
	var data []byte
	var e error
	if r.Method == http.MethodHead {
		a, e = s.media.Metadata(ctx, s.publication, id, publicOnly)
	} else {
		a, data, e = s.media.Read(ctx, s.publication, id, publicOnly)
	}
	if errors.Is(e, media.ErrNotFound) || e == nil && a.Extension != ext {
		failure(w, 404, "NotFoundError", "Image not found")
		return
	}
	if e != nil {
		failure(w, 503, "ServiceUnavailableError", "Image unavailable")
		return
	}
	w.Header().Set("Content-Type", a.MIME)
	w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	w.Header().Set("ETag", `"sha256-`+a.SHA256+`"`)
	if r.Method == http.MethodHead {
		w.Header().Set("X-Content-Media-Profile", "visibility-head-v1")
	}
	w.Header().Set("Content-Disposition", `inline; filename="image.`+a.Extension+`"`)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// No-store keeps authorization/status changes meaningful; an image already
	// downloaded while public cannot be recalled from someone else's device.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}
