// Command devserver runs the review editor against synthetic in-memory posts
// and one generated private image. It listens on loopback only and stores
// nothing. Used by the editor's browser tests.
//
//	go run ./internal/review/editor/devserver -addr 127.0.0.1:0
package main

import (
	"bytes"
	"content/cms"
	"content/internal/review"
	"content/media"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const publication = "synthetic"

type store struct {
	mu    sync.Mutex
	posts map[string]cms.Post
}

func (s *store) Get(_ context.Context, q cms.Query) (cms.Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.posts[q.ID]
	if !ok || q.Publication != publication || (p.TrashedAt != nil) != q.Trashed {
		return cms.Post{}, cms.ErrNotFound
	}
	return p, nil
}

func (s *store) Update(_ context.Context, pub, id string, u cms.UpdatePost) (cms.Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.posts[id]
	if !ok || pub != publication {
		return cms.Post{}, cms.ErrNotFound
	}
	next, err := cms.ApplyUpdate(current, u)
	if err != nil {
		return cms.Post{}, err
	}
	if next.Status == "published" && next.PublishedAt == nil {
		now := time.Now().UTC().Truncate(time.Millisecond)
		next.PublishedAt = &now
	}
	next.Revision++
	next.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if !next.UpdatedAt.After(current.UpdatedAt) {
		next.UpdatedAt = current.UpdatedAt.Add(time.Millisecond)
	}
	s.posts[id] = next
	return next, nil
}

func (s *store) Create(_ context.Context, p cms.Post) (cms.Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.posts[p.ID]; ok {
		return cms.Post{}, cms.ErrConflict
	}
	p.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	p.UpdatedAt = p.CreatedAt
	s.posts[p.ID] = p
	return p, nil
}
func (s *store) Browse(_ context.Context, q cms.Query) ([]cms.Post, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []cms.Post{}
	for _, p := range s.posts {
		if (p.TrashedAt != nil) == q.Trashed && p.Publication == q.Publication && cms.ContentKind(p.Kind) == q.Kind && (q.Status == "" || p.Status == q.Status) && strings.Contains(strings.ToLower(p.Title+" "+p.Slug), strings.ToLower(q.Search)) {
			rows = append(rows, p)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	total := int64(len(rows))
	start := (q.Page - 1) * q.Limit
	if start >= len(rows) {
		return nil, total, nil
	}
	end := start + q.Limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], total, nil
}
func (*store) Ping(context.Context) error { return nil }

type mediaRepo struct {
	mu     sync.Mutex
	assets map[string]media.Asset
}

func (m *mediaRepo) MediaReserve(_ context.Context, a media.Asset) (media.Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.State, a.CreatedAt = "pending", time.Now()
	m.assets[a.ID] = a
	return a, nil
}

func (m *mediaRepo) MediaFinalize(ctx context.Context, _ string, id string, put func(context.Context, media.Asset) error) (media.Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.assets[id]
	if err := put(ctx, a); err != nil {
		return media.Asset{}, err
	}
	a.State = "ready"
	m.assets[id] = a
	return a, nil
}

func (m *mediaRepo) MediaGet(_ context.Context, pub, id string, _ bool) (media.Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok || a.Publication != pub || a.State != "ready" {
		return media.Asset{}, media.ErrNotFound
	}
	return a, nil
}

func (*mediaRepo) MediaCleanup(context.Context, string, time.Time, int, func(context.Context, media.Asset) error) (int, error) {
	return 0, nil
}

type objects struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (o *objects) Put(_ context.Context, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data[key] = bytes.Clone(data)
	return nil
}

func (o *objects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data, ok := o.data[key]
	if !ok {
		return nil, media.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (o *objects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.data, key)
	return nil
}

func syntheticPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(80 + y), 200, 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "loopback listen address")
	validate := flag.Bool("validate", false, "read a JSON array of HTML strings on stdin and report which the CMS accepts")
	flag.Parse()
	if *validate {
		var sources []string
		if err := json.NewDecoder(os.Stdin).Decode(&sources); err != nil {
			log.Fatal(err)
		}
		accepted := make([]bool, len(sources))
		for i, html := range sources {
			_, err := cms.NewPost(publication, cms.CreatePost{Title: "Synthetic", HTML: html})
			accepted[i] = err == nil
		}
		_ = json.NewEncoder(os.Stdout).Encode(accepted)
		return
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	host, _, _ := net.SplitHostPort(listener.Addr().String())
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		log.Fatal("devserver listens on loopback only")
	}
	origin := "http://" + listener.Addr().String()

	password := os.Getenv("REVIEW_DEV_PASSWORD")
	if password == "" {
		password = "synthetic-review-password"
	}
	hash, err := review.HashPassword(password)
	if err != nil {
		log.Fatal(err)
	}
	mediaService, err := media.New(&mediaRepo{assets: map[string]media.Asset{}}, &objects{data: map[string][]byte{}}, origin, "devserver")
	if err != nil {
		log.Fatal(err)
	}
	asset, err := mediaService.Upload(context.Background(), publication, syntheticPNG(), "image/png", "image")
	if err != nil {
		log.Fatal(err)
	}

	posts := map[string]string{
		"rich": `<h2>Synthetic heading</h2><p>Don't lose "quotes", <strong>bold</strong>, <em>italic</em> and <a href="https://example.invalid/" rel="nofollow">a link</a>.</p>` +
			`<ul><li>First item</li><li>Second item</li></ul><ol><li>Numbered</li></ol><blockquote><p>A quotation.</p></blockquote>` +
			`<p><img src="` + mediaService.URL(asset) + `" alt="Synthetic private image"></p><pre><code>code &lt;sample&gt;</code></pre>` +
			`<table><thead><tr><th>Item</th><th>Value</th></tr></thead><tbody><tr><td>A</td><td>1</td></tr></tbody></table><p>Last paragraph.</p>`,
		"lossy": `<p>Water is H<sub>2</sub>O in <abbr title="Synthetic abbreviation">SA</abbr>.</p><dl><dt>Term</dt><dd>Definition</dd></dl><p>Plain paragraph.</p>`,
		"empty": ``,
	}
	s := &store{posts: map[string]cms.Post{}}
	ids := map[string]string{}
	for name, body := range posts {
		p, err := cms.NewPost(publication, cms.CreatePost{Title: "Synthetic " + name + " post", HTML: body})
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		s.posts[p.ID] = p
		ids[name] = p.ID
	}
	server, err := review.New(s, review.Config{Scheduling: true, Origin: origin, Publication: publication, Username: "synthetic", PasswordHash: hash, Media: mediaService})
	if err != nil {
		log.Fatal(err)
	}
	ready, _ := json.Marshal(map[string]any{"origin": origin, "posts": ids, "image": mediaService.URL(asset)})
	fmt.Println(string(ready))
	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if review.Handles(r.URL.Path) {
			server.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})))
}
