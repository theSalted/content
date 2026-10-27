package review

import (
	"content/cms"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
)

func creationMAC(csrf, id, kind string) string {
	h := hmac.New(sha256.New, []byte(csrf))
	h.Write([]byte("new-post\x00" + kind + "\x00" + id))
	return hex.EncodeToString(h.Sum(nil))
}

// GET never creates a record. A session-bound creation ID makes retrying the
// same form open the already-created draft instead of duplicating it.
func (s *Server) newPost(w http.ResponseWriter, r *http.Request, nonce string, v session) {
	kind := r.URL.Query().Get("type")
	if kind == "" {
		kind = "post"
	}
	if kind != "post" && kind != "page" {
		http.Error(w, "Invalid content type", 400)
		return
	}
	p := page{Nonce: nonce, Authenticated: true, CSRF: v.csrf, NewPost: true, KindFilter: kind}
	if r.Method == "GET" {
		id, err := token()
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		p.CreationID = id[:24]
		p.CreationMAC = creationMAC(v.csrf, p.CreationID, kind)
		render(w, 200, p)
		return
	}
	if !s.form(w, r, v) {
		return
	}
	for key, values := range r.PostForm {
		if len(values) != 1 || (key != "csrf" && key != "title" && key != "creation_id" && key != "creation_mac") {
			http.Error(w, "Unsupported form field", 400)
			return
		}
	}
	p.Title = r.PostForm.Get("title")
	p.CreationID = r.PostForm.Get("creation_id")
	p.CreationMAC = r.PostForm.Get("creation_mac")
	if !postPath.MatchString("/ghost/posts/"+p.CreationID+"/") || !equal(p.CreationMAC, creationMAC(v.csrf, p.CreationID, kind)) {
		http.Error(w, "Invalid creation token", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resource := "posts"
	if kind == "page" {
		resource = "pages"
	}
	target := "/ghost/" + resource + "/" + p.CreationID + "/"
	query := cms.Query{Kind: kind, Publication: s.config.Publication, ID: p.CreationID}
	if _, err := s.store.Get(ctx, query); err == nil {
		http.Redirect(w, r, target, 303)
		return
	} else if !errors.Is(err, cms.ErrNotFound) {
		p.Error = "Could not create draft. Please retry."
		render(w, 503, p)
		return
	}
	post, err := cms.NewPost(s.config.Publication, cms.CreatePost{Title: p.Title, Status: "draft", HTML: ""})
	if err != nil {
		p.Error = err.Error()
		render(w, 422, p)
		return
	}
	post.Kind = kind
	post.ID = p.CreationID
	post.Slug = "post-" + post.ID
	if s.config.Author != nil {
		author := *s.config.Author
		post.Author = &author
	}
	if _, err = s.store.Create(ctx, post); err != nil {
		// A simultaneous retry may have committed the same signed creation ID.
		if errors.Is(err, cms.ErrConflict) {
			if _, readErr := s.store.Get(ctx, query); readErr == nil {
				http.Redirect(w, r, target, 303)
				return
			}
		}
		p.Error = "Could not create draft. Please retry."
		render(w, 503, p)
		return
	}
	http.Redirect(w, r, target, 303)
}
