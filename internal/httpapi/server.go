// Package httpapi implements the explicitly limited posts-html-v1 profile.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"content/cms"
	"content/media"
)

type Server struct {
	media              *media.Service
	defaultAuthor      *cms.Author
	publishingIdentity *cms.Author
	store              cms.Store
	publication        string
	siteURL            string
	pageURL            string
	admin              AdminKey
	contentKey         string
	site               SiteMetadata
	scheduling         bool
}

func New(store cms.Store, publication, siteURL string, admin AdminKey, contentKey string, options ...Option) (*Server, error) {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("site URL must be HTTPS (or loopback HTTP), without userinfo/query/fragment")
	}
	if store == nil || publication == "" || len(publication) > 128 || strings.ContainsRune(publication, 0) || admin.id == "" || len(contentKey) < 24 {
		return nil, errors.New("store, publication and caller-supplied API keys are required")
	}
	s := &Server{store: store, publication: publication, siteURL: strings.TrimRight(siteURL, "/"), admin: admin, contentKey: contentKey, site: SiteMetadata{Title: "Content"}}
	for _, option := range options {
		if err := option(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Versioned URL families remain isolated by their JWT audiences. These
	// aliases expose only the same tested HTML/media subset, not full Ghost APIs.
	legacyVersion := ""
	for _, version := range []string{"v2", "v4"} {
		prefix := "/ghost/api/" + version + "/admin/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			legacyVersion = version
			r = r.Clone(r.Context())
			r.URL.Path = "/ghost/api/admin/" + strings.TrimPrefix(r.URL.Path, prefix)
			r.URL.RawPath = ""
			break
		}
	}
	if strings.HasPrefix(r.URL.Path, "/ghost/api/admin/") && !strings.HasSuffix(r.URL.Path, "/") {
		r = r.Clone(r.Context())
		r.URL.Path += "/"
		r.URL.RawPath = ""
	}
	if legacyVersion != "" {
		w.Header().Set("Deprecation", `version="`+legacyVersion+`"`)
		w.Header().Set("Link", "<"+r.URL.EscapedPath()+">; rel=\"latest-version\"")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/content/api/v1/resolve/" {
		s.resolveURL(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		if r.Method != "GET" {
			method(w, "GET")
			return
		}
		reply(w, 200, map[string]string{"status": "alive", "profile": "posts-html-v1"})
		return
	}
	if r.URL.Path == "/readyz" {
		if r.Method != "GET" {
			method(w, "GET")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.store.Ping(ctx) != nil {
			failure(w, 503, "ServiceUnavailableError", "Database is unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
		return
	}
	if r.URL.Path == "/ghost/api/admin/users/me/" {
		s.readCurrentPublishingIdentity(w, r, legacyVersion)
		return
	}
	if r.URL.Path == "/ghost/api/admin/site/" {
		s.readSite(w, r, legacyVersion)
		return
	}
	if r.URL.Path == "/ghost/api/admin/images/upload/" {
		s.uploadImage(w, r, legacyVersion)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/content/images/") {
		s.readImage(w, r)
		return
	}
	resource := "posts"
	if strings.HasPrefix(r.URL.Path, "/ghost/api/admin/pages/") || strings.HasPrefix(r.URL.Path, "/ghost/api/content/pages/") {
		resource = "pages"
	}
	adminPrefix := "/ghost/api/admin/" + resource + "/"
	contentPrefix := "/ghost/api/content/" + resource + "/"
	public := strings.HasPrefix(r.URL.Path, contentPrefix)
	prefix := adminPrefix
	if public {
		prefix = contentPrefix
	}
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/") {
		failure(w, 404, "NotFoundError", "Route not implemented")
		return
	}
	if !profileHeaders(w, r, legacyVersion) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		failure(w, 400, "BadRequestError", "Malformed query")
		return
	}
	for _, values := range query {
		if len(values) != 1 {
			failure(w, 400, "BadRequestError", "Repeated query parameters are unsupported")
			return
		}
	}
	if public {
		if !equalKey(query.Get("key"), s.contentKey) {
			failure(w, 401, "UnauthorizedError", "Invalid content key")
			return
		}
	} else {
		audience := "/admin/"
		if legacyVersion != "" {
			audience = "/" + legacyVersion + "/admin/"
		}
		if s.admin.validateAudience(r.Header.Get("Authorization"), time.Now(), audience) != nil {
			failure(w, 401, "UnauthorizedError", "Invalid admin token")
			return
		}
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	q := cms.Query{Publication: s.publication, PublicOnly: public, Page: 1, Limit: 15}
	if resource == "pages" {
		q.Kind = "page"
		if r.Method != "GET" {
			method(w, "GET")
			return
		}
	}
	if suffix != "" {
		parts := strings.Split(suffix, "/")
		if len(parts) == 1 {
			q.ID = parts[0]
		} else if len(parts) == 2 && parts[0] == "slug" {
			q.Slug = parts[1]
		} else {
			failure(w, 404, "NotFoundError", "Route not implemented")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method == "POST" && !public && suffix == "" {
		s.create(ctx, w, r, query)
		return
	}
	if r.Method == "PUT" && !public && q.ID != "" {
		s.update(ctx, w, r, query, q.ID)
		return
	}
	if r.Method != "GET" {
		allow := "GET"
		if !public && suffix == "" {
			allow = "GET, POST"
		} else if !public && q.ID != "" {
			allow = "GET, PUT"
		}
		method(w, allow)
		return
	}
	allowed := map[string]bool{"formats": true, "fields": true, "include": true}
	if public {
		allowed["key"] = true
	}
	if suffix == "" {
		allowed["page"] = true
		allowed["limit"] = true
	}
	if !validQuery(w, query, allowed) {
		return
	}
	if !validReadProjection(w, query, public) {
		return
	}
	if suffix != "" {
		p, err := s.store.Get(ctx, q)
		if err != nil {
			storeError(w, err)
			return
		}
		reply(w, 200, map[string]any{resource: []any{s.projectRead(p, public, query)}})
		return
	}
	if v := query.Get("page"); v != "" {
		q.Page, err = strconv.Atoi(v)
		if err != nil || q.Page < 1 || q.Page > 1000000 {
			failure(w, 400, "BadRequestError", "Invalid page")
			return
		}
	}
	if v := query.Get("limit"); v != "" {
		if v == "all" {
			q.Limit = 100
		} else {
			q.Limit, err = strconv.Atoi(v)
			if err != nil || q.Limit < 1 {
				failure(w, 400, "BadRequestError", "Invalid limit")
				return
			}
			if q.Limit > 100 {
				q.Limit = 100
			}
		}
	}
	posts, total, err := s.store.Browse(ctx, q)
	if err != nil {
		storeError(w, err)
		return
	}
	out := []any{}
	for _, p := range posts {
		out = append(out, s.projectRead(p, public, query))
	}
	pages := (total + int64(q.Limit) - 1) / int64(q.Limit)
	var next, prev any
	if int64(q.Page) < pages {
		next = q.Page + 1
	}
	if q.Page > 1 {
		prev = q.Page - 1
	}
	reply(w, 200, map[string]any{resource: out, "meta": map[string]any{"pagination": map[string]any{"page": q.Page, "limit": q.Limit, "pages": pages, "total": total, "next": next, "prev": prev}}})
}
func (s *Server) create(ctx context.Context, w http.ResponseWriter, r *http.Request, q url.Values) {
	if !validQuery(w, q, map[string]bool{"source": true, "formats": true}) {
		return
	}
	if q.Get("source") != "html" || (q.Get("formats") != "" && q.Get("formats") != "html") {
		failure(w, 400, "BadRequestError", "Creation requires source=html; only HTML is implemented")
		return
	}
	var envelope struct {
		Posts []struct {
			cms.CreatePost
			Tags        json.RawMessage `json:"tags"`
			Featured    json.RawMessage `json:"featured"`
			PublishedAt json.RawMessage `json:"published_at"`
		} `json:"posts"`
	}
	if !decodeBody(w, r, &envelope) {
		return
	}
	if len(envelope.Posts) != 1 {
		failure(w, 422, "ValidationError", "Exactly one post is required")
		return
	}
	// iA Writer sends an empty tags array even for untagged HTML drafts.
	// Accept that lossless no-relation case; nonempty/invalid relations still fail.
	if raw := envelope.Posts[0].Tags; len(raw) != 0 {
		var tags []json.RawMessage
		if json.Unmarshal(raw, &tags) != nil || strings.TrimSpace(string(raw)) == "null" || len(tags) != 0 {
			failure(w, 422, "ValidationError", "Only an empty tags array is supported")
			return
		}
	}
	payload := envelope.Posts[0]
	featured, valid := optionalFeatured(w, payload.Featured)
	if !valid {
		return
	}
	if !emptyPublicationDate(w, payload.PublishedAt) {
		return
	}
	payload.CreatePost.Featured = featured
	p, err := cms.NewPost(s.publication, payload.CreatePost)
	if err != nil {
		logContentRejection(payload.CreatePost)
		storeError(w, err)
		return
	}
	if s.defaultAuthor != nil {
		a := *s.defaultAuthor
		p.Author = &a
	}
	saved, err := s.store.Create(ctx, p)
	if err != nil {
		storeError(w, err)
		return
	}
	w.Header().Set("Location", "/ghost/api/admin/posts/"+saved.ID+"/")
	reply(w, 201, map[string]any{"posts": []any{s.project(saved, false)}})
}
func (s *Server) project(p cms.Post, public bool) map[string]any {
	var published any
	if p.PublishedAt != nil {
		published = timestamp(*p.PublishedAt)
	}
	if !public && p.Status == "scheduled" && p.ScheduledFor != nil {
		published = timestamp(*p.ScheduledFor)
	}
	featured := false
	_ = json.Unmarshal(p.Presentation["featured"], &featured)
	base := s.siteURL
	if p.Kind == "page" && s.pageURL != "" {
		base = s.pageURL
	}
	u, _ := url.JoinPath(base, p.Slug)
	u += "/"
	body := p.HTML
	if s.media != nil {
		body = s.media.ProjectHTML(body)
	}
	dto := map[string]any{"id": p.ID, "uuid": p.UUID, "title": p.Title, "slug": p.Slug, "html": body, "visibility": p.Visibility, "created_at": timestamp(p.CreatedAt), "updated_at": timestamp(p.UpdatedAt), "published_at": published, "url": u, "featured": featured}
	if p.Author != nil {
		dto["primary_author"] = *p.Author
		dto["authors"] = []cms.Author{*p.Author}
	}
	if !public {
		dto["status"] = p.Status
		dto["tags"] = []any{}

	}
	return dto
}
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func validQuery(w http.ResponseWriter, q url.Values, allowed map[string]bool) bool {
	for k := range q {
		if !allowed[k] {
			failure(w, 400, "BadRequestError", "Unsupported query parameter")
			return false
		}
	}
	return true
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	failure(w, 405, "MethodNotAllowedError", "Method not implemented")
}
func storeError(w http.ResponseWriter, err error) {
	var invalid *cms.InvalidInput
	switch {
	case errors.Is(err, cms.ErrNotFound):
		failure(w, 404, "NotFoundError", "Post not found")
	case errors.Is(err, cms.ErrArchivePending):
		failure(w, http.StatusConflict, "ConflictError", "Post archive delivery is pending; retry after acknowledgment.")
	case errors.Is(err, cms.ErrStaleUpdate):
		reply(w, 409, map[string]any{"errors": []any{map[string]string{"message": "Saving failed! Someone else is editing this post.", "errorType": "UpdateCollisionError", "code": "UPDATE_COLLISION"}}})
	case errors.Is(err, cms.ErrConflict):
		failure(w, 409, "ConflictError", "Slug already exists")
	case errors.As(err, &invalid):
		logValidationReason(invalid.Message)
		failure(w, 422, "ValidationError", invalid.Message)
	default:
		failure(w, 500, "InternalServerError", "The operation failed")
	}
}
func failure(w http.ResponseWriter, status int, kind, message string) {
	if status == 400 {
		logRequestRejection(message)
	}
	reply(w, status, map[string]any{"errors": []any{map[string]string{"message": message, "errorType": kind}}})
}
func reply(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		failure(w, 415, "UnsupportedMediaTypeError", "Expected application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var large *http.MaxBytesError
		if errors.As(readErr, &large) {
			failure(w, 413, "RequestEntityTooLargeError", "Request exceeds 2 MiB")
		} else {
			failure(w, 400, "BadRequestError", "Could not read JSON")
		}
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(value); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			failure(w, 413, "RequestEntityTooLargeError", "Request exceeds 2 MiB")
		} else {
			logBodyRejection(raw, "invalid_json_or_unsupported_field")
			failure(w, 400, "BadRequestError", "Invalid JSON or unsupported field")
		}
		return false
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		logBodyRejection(nil, "multiple_json_documents")
		failure(w, 400, "BadRequestError", "Expected one JSON document")
		return false
	}
	return true
}

// Raw fields distinguish omission from explicit null and empty strings.
type updatePayload struct {
	Featured    json.RawMessage `json:"featured"`
	PublishedAt json.RawMessage `json:"published_at"`
	Tags        json.RawMessage `json:"tags"`
	UpdatedAt   string          `json:"updated_at"`
	Title       json.RawMessage `json:"title"`
	Slug        json.RawMessage `json:"slug"`
	HTML        json.RawMessage `json:"html"`
	Status      json.RawMessage `json:"status"`
	Visibility  json.RawMessage `json:"visibility"`
}

func (s *Server) update(ctx context.Context, w http.ResponseWriter, r *http.Request, q url.Values, id string) {
	if !validQuery(w, q, map[string]bool{"source": true, "formats": true, "save_revision": true}) {
		return
	}
	if v, ok := q["source"]; ok && v[0] != "html" {
		failure(w, 400, "BadRequestError", "Only source=html is implemented")
		return
	}
	if v := q.Get("formats"); v != "" && v != "html" {
		failure(w, 400, "BadRequestError", "Only formats=html is implemented")
		return
	}
	if v, ok := q["save_revision"]; ok && v[0] != "true" {
		failure(w, 400, "BadRequestError", "Only save_revision=true is supported; internal revisions are always retained")
		return
	}
	var envelope struct {
		Posts []updatePayload `json:"posts"`
	}
	if !decodeBody(w, r, &envelope) {
		return
	}
	if len(envelope.Posts) != 1 {
		failure(w, 422, "ValidationError", "Exactly one post is required")
		return
	}
	payload := envelope.Posts[0]
	token, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		failure(w, 422, "ValidationError", "updated_at is required and must be an RFC3339 timestamp")
		return
	}
	featured, valid := optionalFeatured(w, payload.Featured)
	if !valid {
		return
	}
	publicationDate, valid := parsePublicationDate(w, payload.PublishedAt, s.scheduling)
	if !valid {
		return
	}
	if payload.Tags != nil {
		var tags []json.RawMessage
		if json.Unmarshal(payload.Tags, &tags) != nil || strings.TrimSpace(string(payload.Tags)) == "null" || len(tags) != 0 {
			failure(w, 422, "ValidationError", "Only an empty tags array is supported")
			return
		}
	}
	in := cms.UpdatePost{ExpectedUpdatedAt: token, Featured: featured, ClearTags: payload.Tags != nil, PublishedAt: publicationDate, RequireEmptyPublishedAt: payload.PublishedAt != nil && publicationDate == nil}
	for _, pair := range []struct {
		raw  json.RawMessage
		dest **string
	}{{payload.Title, &in.Title}, {payload.Slug, &in.Slug}, {payload.HTML, &in.HTML}, {payload.Status, &in.Status}, {payload.Visibility, &in.Visibility}} {
		if pair.raw == nil {
			continue
		}
		var value *string
		if err = json.Unmarshal(pair.raw, &value); err != nil || value == nil {
			failure(w, 400, "BadRequestError", "Editable fields must be strings; null is unsupported")
			return
		}
		*pair.dest = value
	}
	if in.HTML != nil && q.Get("source") != "html" {
		failure(w, 400, "BadRequestError", "HTML edits require source=html")
		return
	}
	if in.Status != nil && *in.Status == "scheduled" && !s.scheduling {
		failure(w, 422, "ValidationError", "Scheduling is disabled")
		return
	}
	if err = in.ValidateToken(); err != nil {
		storeError(w, err)
		return
	}
	saved, err := s.store.Update(ctx, s.publication, id, in)
	if err != nil {
		storeError(w, err)
		return
	}
	reply(w, 200, map[string]any{"posts": []any{s.project(saved, false)}})
}
