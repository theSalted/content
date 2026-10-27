package review

import (
	"content/cms"
	"content/media"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Origin, Publication, Username, PasswordHash string
	Media                                       *media.Service
	Author                                      *cms.Author
	Sessions                                    cms.BrowserSessionStore
	OriginAliases                               []string
	Scheduling, SlugRedirects                   bool
}
type session struct {
	csrf          string
	authenticated bool
	expires       time.Time
}
type attempt struct {
	count   int
	expires time.Time
}
type Server struct {
	store       cms.Store
	config      Config
	digest      passwordDigest
	cookie      string
	secure      bool
	mu          sync.Mutex
	sessions    map[string]session
	attempts    map[string]attempt
	verifySlots chan struct{}
	now         func() time.Time
}

var postPath = regexp.MustCompile(`^/ghost/(?:posts|pages)/([0-9a-f]{24})/$`)
var username = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

func New(store cms.Store, c Config) (*Server, error) {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, errors.New("review origin must be HTTPS or loopback HTTP, without a path")
	}
	if store == nil || c.Publication == "" || !username.MatchString(c.Username) {
		return nil, errors.New("review store, publication and operator username required")
	}
	d, err := parseDigest(c.PasswordHash)
	if err != nil {
		return nil, err
	}
	c.Origin = u.Scheme + "://" + u.Host
	if len(c.OriginAliases) > 8 {
		return nil, errors.New("at most eight review origin aliases")
	}
	aliases := make([]string, 0, len(c.OriginAliases))
	for _, value := range c.OriginAliases {
		alias, err := media.NormalizeOrigin(value)
		if err != nil {
			return nil, errors.New("invalid review origin alias")
		}
		if u.Scheme == "https" && !strings.HasPrefix(alias, "https://") {
			return nil, errors.New("HTTPS review requires HTTPS aliases")
		}
		aliases = append(aliases, alias)
	}
	c.OriginAliases = aliases
	if c.Sessions != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Sessions.CheckBrowserSessions(ctx); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256([]byte(c.Publication))
	return &Server{store: store, config: c, digest: d, cookie: fmt.Sprintf("content_review_%x", sum[:6]), secure: u.Scheme == "https", sessions: map[string]session{}, attempts: map[string]attempt{}, verifySlots: make(chan struct{}, 4), now: time.Now}, nil
}
func Handles(path string) bool {
	return path == "/ghost/" || path == "/ghost/login/" || path == "/ghost/logout/" || strings.HasPrefix(path, "/ghost/posts/") || strings.HasPrefix(path, "/ghost/pages/") || strings.HasPrefix(path, "/ghost/images/") || strings.HasPrefix(path, "/ghost/assets/")
}
func token() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (s *Server) current(r *http.Request) (string, session, bool) {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return "", session{}, false
	}
	if s.config.Sessions != nil {
		if len(c.Value) != 64 {
			return "", session{}, false
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		stored, err := s.config.Sessions.LoadBrowserSession(ctx, s.sessionScope(), sessionHash(c.Value), s.now())
		if err != nil {
			return "", session{}, false
		}
		return c.Value, session{csrf: sessionCSRF(c.Value), authenticated: stored.Authenticated, expires: stored.Expires}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[c.Value]
	if ok && !s.now().Before(v.expires) {
		delete(s.sessions, c.Value)
		ok = false
	}
	return c.Value, v, ok
}
func (s *Server) issue(w http.ResponseWriter, old string, authenticated bool) (session, error) {
	id, err := token()
	if err != nil {
		return session{}, err
	}
	csrf, err := token()
	if err != nil {
		return session{}, err
	}
	ttl := 10 * time.Minute
	if authenticated {
		ttl = 8 * time.Hour
	}
	v := session{csrf, authenticated, s.now().Add(ttl)}
	if s.config.Sessions != nil {
		v.csrf = sessionCSRF(id)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.config.Sessions.SaveBrowserSession(ctx, s.sessionScope(), sessionHash(id), sessionHash(old), cms.BrowserSession{Authenticated: authenticated, Expires: v.expires}, s.now()); err != nil {
			return session{}, errors.New("session store unavailable")
		}
		http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id, Path: "/ghost/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
		return v, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, x := range s.sessions {
		if !s.now().Before(x.expires) {
			delete(s.sessions, k)
		}
	}
	delete(s.sessions, old)
	if len(s.sessions) >= 1024 {
		return session{}, errors.New("session capacity reached")
	}
	s.sessions[id] = v
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id, Path: "/ghost/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
	return v, nil
}
func (s *Server) form(w http.ResponseWriter, r *http.Request, v session) bool {
	origin := r.Header.Get("Origin")
	trusted := origin == s.config.Origin
	for _, alias := range s.config.OriginAliases {
		trusted = trusted || origin == alias
	}
	if !trusted {
		http.Error(w, "Origin rejected", 403)
		return false
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", 415)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid or oversized form", 400)
		return false
	}
	if len(r.PostForm["csrf"]) != 1 || !equal(r.PostForm.Get("csrf"), v.csrf) {
		http.Error(w, "Invalid form token", 403)
		return false
	}
	return true
}
func nextPath(value string) string {
	if postPath.MatchString(value) {
		return value
	}
	if u, err := url.Parse(value); err == nil && u.Scheme == "" && u.Host == "" && u.Path == "/ghost/posts/" && u.Fragment == "" && u.RawPath == "" {
		if options, err := parseList(u.RawQuery); err == nil {
			return options.URL(options.Page)
		}
	}
	return "/ghost/posts/"
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/ghost/assets/") {
		serveAsset(w, r)
		return
	}
	nonce, err := token()
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// no-referrer makes native form POST Origin null. Keep exact Origin checks
	// and suppress cross-origin referrers while preserving same-origin forms.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method != "GET" && r.Method != "POST" {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", 405)
		return
	}
	id, v, ok := s.current(r)
	switch r.URL.Path {
	case "/ghost/":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		render(w, 200, page{Nonce: nonce, Landing: true, Authenticated: ok && v.authenticated, CSRF: v.csrf})
		return
	case "/ghost/posts/new/":
		if !ok || !v.authenticated {
			http.Redirect(w, r, "/ghost/login/?next="+url.QueryEscape("/ghost/posts/"), 303)
			return
		}
		s.newPost(w, r, nonce, v)
		return
	case "/ghost/posts/":
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method not allowed", 405)
			return
		}
		if !ok || !v.authenticated {
			http.Redirect(w, r, "/ghost/login/?next="+url.QueryEscape(nextPath(r.URL.RequestURI())), 303)
			return
		}
		s.listPosts(w, r, nonce, v)
		return
	case "/ghost/login/":
		s.login(w, r, nonce, id, v, ok)
		return
	case "/ghost/logout/":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if !ok || !v.authenticated {
			http.Error(w, "Sign in required", 401)
			return
		}
		if !s.form(w, r, v) {
			return
		}
		if s.config.Sessions != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			if err := s.config.Sessions.DeleteBrowserSession(ctx, s.sessionScope(), sessionHash(id)); err != nil {
				http.Error(w, "Could not sign out. Please retry.", 503)
				return
			}
		} else {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: "", Path: "/ghost/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		http.Redirect(w, r, "/ghost/login/", 303)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/ghost/images/") {
		if r.Method != "GET" || !ok || !v.authenticated {
			http.Error(w, "Sign in required", 401)
			return
		}
		imageID, ext, valid := media.ImagePath(strings.Replace(r.URL.Path, "/ghost/images/", "/content/images/", 1))
		if !valid || r.URL.RawQuery != "" || s.config.Media == nil {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		asset, data, e := s.config.Media.Read(ctx, s.config.Publication, imageID, false)
		if e != nil || asset.Extension != ext {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.MIME)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	match := postPath.FindStringSubmatch(r.URL.Path)
	if match == nil {
		http.NotFound(w, r)
		return
	}
	if !ok || !v.authenticated {
		if r.Method == "GET" {
			http.Redirect(w, r, "/ghost/login/?next="+url.QueryEscape(r.URL.Path), 303)
		} else {
			http.Error(w, "Session expired; sign in before retrying. Your submitted changes were not saved.", 401)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	kind := "post"
	if strings.HasPrefix(r.URL.Path, "/ghost/pages/") {
		kind = "page"
	}
	post, err := s.store.Get(ctx, cms.Query{Kind: kind, Publication: s.config.Publication, ID: match[1]})
	if errors.Is(err, cms.ErrNotFound) {
		post, err = s.store.Get(ctx, cms.Query{Kind: kind, Publication: s.config.Publication, ID: match[1], Trashed: true})
	}
	if err != nil {
		if errors.Is(err, cms.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Could not load draft", 503)
		}
		return
	}
	p := page{Path: r.URL.Path, Nonce: nonce, Authenticated: true, CSRF: v.csrf, ID: post.ID, Title: post.Title, Body: post.SourceHTML, Updated: post.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"), Status: post.Status, Slug: post.Slug, OriginalSlug: post.Slug, PublishedTime: publicationInput(post.PublishedAt), OriginalPublishedTime: publicationInput(post.PublishedAt), HasPublished: post.PublishedAt != nil, Trashed: post.TrashedAt != nil, Post: true, Scheduling: s.config.Scheduling, SlugRedirects: s.config.SlugRedirects, ScheduleTime: publicationInput(post.ScheduledFor), ScheduleDisplay: scheduleDisplay(post.ScheduledFor)}
	if s.config.Media != nil {
		origins := append([]string{s.config.Media.Origin}, s.config.Media.OriginAliases...)
		encoded, _ := json.Marshal(origins)
		p.MediaOrigins = string(encoded)
	}
	status := 200
	if r.Method == "POST" {
		if !s.form(w, r, v) {
			return
		}
		for k, values := range r.PostForm {
			if (k != "csrf" && k != "title" && k != "html" && k != "updated_at" && k != "action" && k != "slug" && k != "published_time" && k != "confirm_slug_change" && k != "confirm_unpublish" && k != "confirm_trash" && k != "scheduled_time") || len(values) != 1 {
				http.Error(w, "Unsupported form field", 400)
				return
			}
		}
		if action := r.PostForm.Get("action"); action == "trash" || action == "restore" {
			when, e := time.Parse(time.RFC3339Nano, r.PostForm.Get("updated_at"))
			if e != nil || (action == "trash" && r.PostForm.Get("confirm_trash") != "yes") {
				p.Error = "Confirm moving this post to Trash. It will be unpublished and can be restored."
				render(w, 422, p)
				return
			}
			trash := action == "trash"
			_, e = s.store.Update(ctx, s.config.Publication, post.ID, cms.UpdatePost{Kind: post.Kind, ExpectedUpdatedAt: when, Trash: &trash})
			if e != nil {
				code := 503
				p.Error = "Could not change this post. Please reload and retry."
				if errors.Is(e, cms.ErrStaleUpdate) {
					code = 409
					p.Error = "This post changed elsewhere. Reload before changing its state."
				}
				render(w, code, p)
				return
			}
			http.Redirect(w, r, r.URL.Path, 303)
			return
		}
		p.Title, p.Body, p.Updated = r.PostForm.Get("title"), r.PostForm.Get("html"), r.PostForm.Get("updated_at")
		when, e := time.Parse(time.RFC3339Nano, p.Updated)
		action := r.PostForm.Get("action")
		if e != nil || (action != "save" && action != "publish" && action != "unpublish" && action != "schedule" && action != "cancel_schedule") {
			p.Error = "Invalid save request. Your changes remain below."
			status = 422
		} else {
			update := cms.UpdatePost{Kind: post.Kind, ExpectedUpdatedAt: when, Title: &p.Title, HTML: &p.Body}
			if value, ok := r.PostForm["slug"]; ok {
				p.Slug = value[0]
				update.Slug = &p.Slug
			}
			if value, ok := r.PostForm["published_time"]; ok {
				p.PublishedTime = value[0]
			}
			if !s.config.SlugRedirects && p.Slug != post.Slug && post.PublishedAt != nil && r.PostForm.Get("confirm_slug_change") != "yes" {
				p.Error = "Changing the slug changes its URL. Old links will not redirect automatically. Confirm the URL change to continue."
				render(w, 422, p)
				return
			}
			if p.PublishedTime != publicationInput(post.PublishedAt) {
				if post.Status != "published" {
					p.Error = "Publish date can only be edited on a published post."
					render(w, 422, p)
					return
				}
				value, err := time.Parse("2006-01-02T15:04:05.999", p.PublishedTime)
				if err != nil {
					value, err = time.Parse("2006-01-02T15:04", p.PublishedTime)
				}
				if err != nil || cms.ValidatePublicationDate(value, s.now()) != nil {
					p.Error = "Publish date must be in the past or present (UTC). To schedule, use Publish on a draft."
					render(w, 422, p)
					return
				}
				update.PublishedAt = &value
			}
			if value, ok := r.PostForm["scheduled_time"]; ok {
				p.ScheduleTime = value[0]
			}
			if action == "schedule" {
				if !s.config.Scheduling {
					p.Error = "Scheduling is disabled."
					render(w, 422, p)
					return
				}
				if post.Status == "published" {
					p.Error = "Unpublish before scheduling."
					render(w, 422, p)
					return
				}
				due, e := time.Parse("2006-01-02T15:04", p.ScheduleTime)
				if e != nil || cms.ValidateScheduleDate(due, s.now()) != nil {
					p.Error = "Enter a future schedule time in UTC."
					render(w, 422, p)
					return
				}
				scheduled := "scheduled"
				update.Status = &scheduled
				update.PublishedAt = &due
			} else if _, sent := r.PostForm["scheduled_time"]; sent {
				p.Error = "Choose Schedule to change the scheduled publication time."
				render(w, 422, p)
				return
			}
			if action == "cancel_schedule" {
				if post.Status != "scheduled" {
					p.Error = "This post has no active schedule."
					render(w, 422, p)
					return
				}
				draft := "draft"
				update.Status = &draft
				update.PublishedAt = nil
			}
			if post.Status == "draft" && action == "save" && p.ScheduleTime != "" {
				p.Error = "Choose Schedule to schedule this post, or clear the schedule time."
				render(w, 422, p)
				return
			}
			if action == "unpublish" {
				if r.PostForm.Get("confirm_unpublish") != "yes" {
					p.Error = "Confirm unpublishing to remove this post from public view."
					render(w, 422, p)
					return
				}
				draft := "draft"
				update.Status = &draft
			}
			if action == "publish" {
				if post.Status == "scheduled" {
					update.PublishedAt = nil
				}
				published := "published"
				update.Status = &published
			}
			changed, e := s.store.Update(ctx, s.config.Publication, post.ID, update)
			if e == nil {
				p.Title, p.Body, p.Updated, p.Status = changed.Title, changed.SourceHTML, changed.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"), changed.Status
				p.Slug, p.OriginalSlug, p.PublishedTime, p.OriginalPublishedTime, p.HasPublished = changed.Slug, changed.Slug, publicationInput(changed.PublishedAt), publicationInput(changed.PublishedAt), changed.PublishedAt != nil
				p.ScheduleTime = publicationInput(changed.ScheduledFor)
				p.ScheduleDisplay = scheduleDisplay(changed.ScheduledFor)
				p.Notice = "Saved."
				if action == "schedule" {
					p.Notice = "Scheduled."
				}
				if action == "cancel_schedule" {
					p.Notice = "Schedule cancelled."
				}
				if action == "unpublish" {
					p.Notice = "Unpublished."
				}
				if action == "publish" {
					p.Notice = "Published."
				}
			} else {
				status = 503
				p.Error = "Could not save. Your changes remain below; retry when the service is available."
				var invalid *cms.InvalidInput
				if errors.Is(e, cms.ErrStaleUpdate) {
					status = 409
					p.Error = "This post changed elsewhere. Your edits remain below. Open the latest saved version in a new tab before merging; this form retains its original revision."
				} else if errors.Is(e, cms.ErrArchivePending) {
					status = 409
					p.Error = "This post is waiting for its archive acknowledgment. Your changes remain below; retry after delivery completes."
				} else if errors.Is(e, cms.ErrConflict) {
					status = 409
					p.Error = "This slug is already in use. Your edits remain below."
				} else if errors.As(e, &invalid) {
					status = 422
					p.Error = invalid.Message + " Your changes remain below."
				}
			}
		}
	}
	render(w, status, p)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request, nonce, id string, v session, ok bool) {
	next := nextPath(r.URL.Query().Get("next"))
	if ok && v.authenticated {
		http.Redirect(w, r, next, 303)
		return
	}
	if !ok {
		var err error
		v, err = s.issue(w, id, false)
		if err != nil {
			http.Error(w, "Please retry later", 503)
			return
		}
	}
	p := page{Nonce: nonce, Login: true, CSRF: v.csrf, Next: next}
	if r.Method == "GET" {
		render(w, 200, p)
		return
	}
	if !s.form(w, r, v) {
		return
	}
	for k, values := range r.PostForm {
		if (k != "csrf" && k != "username" && k != "password") || len(values) != 1 {
			http.Error(w, "Unsupported login field", 400)
			return
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	s.mu.Lock()
	now := s.now()
	for k, a := range s.attempts {
		if !now.Before(a.expires) {
			delete(s.attempts, k)
		}
	}
	a := s.attempts[ip]
	limited := a.count >= 5 || len(s.attempts) >= 1024
	if !limited {
		if a.count == 0 {
			a.expires = now.Add(15 * time.Minute)
		}
		a.count++
		s.attempts[ip] = a
	}
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "900")
		p.Error = "Too many attempts. Try again later."
		render(w, 429, p)
		return
	}
	select {
	case s.verifySlots <- struct{}{}:
	default:
		p.Error = "Sign-in is busy. Try again later."
		render(w, 429, p)
		return
	}
	valid := s.digest.matches(r.PostForm.Get("password"))
	<-s.verifySlots
	if !valid || !equal(r.PostForm.Get("username"), s.config.Username) {
		p.Error = "Username or password is incorrect."
		render(w, 401, p)
		return
	}
	s.mu.Lock()
	delete(s.attempts, ip)
	s.mu.Unlock()
	if _, err = s.issue(w, id, true); err != nil {
		http.Error(w, "Please retry later", 503)
		return
	}
	http.Redirect(w, r, next, 303)
}

type page struct {
	Scheduling, SlugRedirects                                                bool
	ScheduleTime, ScheduleDisplay                                            string
	Path, Nonce, CSRF, Next, ID, Title, Body, Updated, Status, Error, Notice string
	MediaOrigins                                                             string
	Landing, Login, Post, Authenticated, PostsList                           bool
	Posts                                                                    []postRow
	ListPage, ListPages                                                      int
	Previous, NextPage                                                       string
	SearchFilter, StatusFilter, KindFilter                                   string
	Slug, OriginalSlug, PublishedTime, OriginalPublishedTime                 string
	HasPublished, NewPost, Trashed                                           bool
	CreationID, CreationMAC                                                  string
}

func render(w http.ResponseWriter, status int, p page) {
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, p)
}

var pageTemplate = template.Must(template.New("review").Parse(strings.Replace(pageHTML, editorScriptMarker, editorPath, 1)))

// serveAsset serves the editor bundle. It holds no private data, so it needs
// no session; its content-addressed path makes it safe to cache forever.
func serveAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != editorPath || r.URL.RawQuery != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Length", strconv.Itoa(len(editorJS)))
	w.WriteHeader(200)
	if r.Method == "GET" {
		_, _ = w.Write(editorJS)
	}
}

func publicationInput(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04")
}

func sessionHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func sessionCSRF(cookie string) string { return sessionHash("content-csrf\x00" + cookie) }
func (s *Server) sessionScope() string {
	return sessionHash("content-session\x00" + s.config.Publication + "\x00" + s.config.Username + "\x00" + s.config.PasswordHash)
}

func scheduleDisplay(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format("02 Jan 2006, 15:04 UTC")
}
