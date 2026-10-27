package review

import (
	"content/cms"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const postsPerPage = 20

type postRow struct {
	Path, Title, Status, Date, DateTime string
}

type listOptions struct {
	Page                 int
	Search, Status, Kind string
}

func parseList(raw string) (listOptions, error) {
	o := listOptions{Page: 1, Kind: "post"}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return o, err
	}
	for key, items := range values {
		if (key != "page" && key != "q" && key != "status" && key != "type") || len(items) != 1 {
			return o, errors.New("invalid filter")
		}
	}
	if _, ok := values["page"]; ok {
		o.Page, err = strconv.Atoi(values.Get("page"))
		if err != nil || o.Page < 1 || o.Page > 1000000 {
			return o, errors.New("invalid page")
		}
	}
	o.Search = strings.TrimSpace(values.Get("q"))
	o.Status = values.Get("status")
	if value := values.Get("type"); value != "" {
		o.Kind = value
	}
	if len(o.Search) > 200 || !utf8.ValidString(o.Search) || strings.ContainsRune(o.Search, 0) || (o.Status != "" && o.Status != "draft" && o.Status != "published" && o.Status != "scheduled" && o.Status != "trash") || (o.Kind != "post" && o.Kind != "page") {
		return o, errors.New("invalid filter")
	}
	return o, nil
}
func listPage(raw string) (int, error) { o, err := parseList(raw); return o.Page, err }
func (o listOptions) URL(number int) string {
	q := url.Values{}
	if number > 1 {
		q.Set("page", strconv.Itoa(number))
	}
	if o.Search != "" {
		q.Set("q", o.Search)
	}
	if o.Status != "" {
		q.Set("status", o.Status)
	}
	if o.Kind == "page" {
		q.Set("type", "page")
	}
	if len(q) == 0 {
		return "/ghost/posts/"
	}
	return "/ghost/posts/?" + q.Encode()
}

func postsURL(number int) string {
	if number <= 1 {
		return "/ghost/posts/"
	}
	return "/ghost/posts/?page=" + strconv.Itoa(number)
}

// Called only after browser authentication. Listing uses the existing read-only,
// publication-scoped browse transaction and never grants integration-key access.
func (s *Server) listPosts(w http.ResponseWriter, r *http.Request, nonce string, v session) {
	options, err := parseList(r.URL.RawQuery)
	number := options.Page
	if err != nil {
		http.Error(w, "Invalid page", http.StatusBadRequest)
		return
	}
	p := page{Nonce: nonce, Authenticated: true, CSRF: v.csrf, PostsList: true, ListPage: number, SearchFilter: options.Search, StatusFilter: options.Status, KindFilter: options.Kind}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status := options.Status
	if status == "trash" {
		status = ""
	}
	posts, total, err := s.store.Browse(ctx, cms.Query{Kind: options.Kind, Publication: s.config.Publication, Page: number, Limit: postsPerPage, Status: status, Trashed: options.Status == "trash", Search: options.Search})
	if err != nil {
		p.Error = "Could not load posts. Please reload to try again."
		render(w, http.StatusServiceUnavailable, p)
		return
	}
	pages := total / postsPerPage
	if total%postsPerPage != 0 {
		pages++
	}
	if pages < 1 {
		pages = 1
	}
	if pages > 1000000 {
		pages = 1000000
	}
	p.ListPages = int(pages)
	if number > p.ListPages {
		http.Redirect(w, r, options.URL(p.ListPages), http.StatusSeeOther)
		return
	}
	for _, post := range posts {
		status := "Draft"
		if post.TrashedAt != nil {
			status = "Trash"
		} else if post.Status == "published" {
			status = "Published"
		} else if post.Status == "scheduled" {
			status = "Scheduled"
		}
		resource := "posts"
		if options.Kind == "page" {
			resource = "pages"
		}
		p.Posts = append(p.Posts, postRow{Path: "/ghost/" + resource + "/" + post.ID + "/", Title: post.Title, Status: status, Date: post.UpdatedAt.UTC().Format("02 Jan 2006"), DateTime: post.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	if number > 1 {
		p.Previous = options.URL(number - 1)
	}
	if number < p.ListPages {
		p.NextPage = options.URL(number + 1)
	}
	render(w, http.StatusOK, p)
}
