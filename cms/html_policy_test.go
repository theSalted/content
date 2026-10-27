package cms

import "testing"

func TestGhostInertHTMLPreserved(t *testing.T) {
	source := `<!--kg-card-begin: html--><a></a><h2 id="">Empty anchor</h2><!--kg-card-end: html--><a href="https://example.invalid/">Link</a><h2 id="section-one%20two">Heading</h2><figure class="kg-card kg-image-card"><img src="https://example.invalid/photo.jpg" class="kg-image" loading="lazy" alt="Image" srcset="https://example.invalid/small.jpg 300w, https://example.invalid/large.jpg 1000w" sizes="(min-width: 720px) 720px, 100vw"></figure><pre><code class="language-go">code</code></pre>`
	p, e := NewPost("publication", CreatePost{Title: "Image", HTML: source})
	if e != nil || p.HTML != source || p.SourceHTML != source {
		t.Fatal(e)
	}
}

func TestHTMLExpansionDoesNotAllowActiveContent(t *testing.T) {
	for _, source := range []string{`<img src="https://example.invalid/a" srcset="javascript:alert(1) 1x">`, `<img src="https://example.invalid/a" srcset="data:text/html,bad 1x">`, `<img src="https://example.invalid/a" onerror="alert(1)">`, `<span style="background:url(javascript:alert(1))">x</span>`, `<iframe src="https://www.youtube.com/embed/test"></iframe>`, `<h2 id="x" onclick="alert(1)">x</h2>`, `<svg onload="alert(1)"></svg>`} {
		if _, e := NewPost("publication", CreatePost{Title: "Unsafe", HTML: source}); e == nil {
			t.Fatal("active HTML accepted", source)
		}
	}
}

func TestGhostDraftTableAndEmptyFragment(t *testing.T) {
	source := `<table><thead><tr><th style="text-align: left;">Heading</th></tr></thead><tbody><tr><td style="text-align: right;">Cell</td></tr></tbody></table><p><a href="https://example.org/article/#">Link</a></p>`
	p, e := NewPost("p", CreatePost{Title: "Draft", HTML: source})
	if e != nil || p.HTML != source {
		t.Fatal("safe source changed or rejected", e)
	}
	for _, unsafe := range []string{`<td style="text-align: left; background: url(https://example.org/track)">Cell</td>`, `<a href="javascript:alert(1)#">Link</a>`} {
		if _, e = NewPost("p", CreatePost{Title: "Draft", HTML: unsafe}); e == nil {
			t.Fatal("unsafe markup accepted")
		}
	}
}
