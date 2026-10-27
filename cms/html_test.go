package cms

import "testing"

func TestIAImageVoidSpacing(t *testing.T) {
	for _, source := range []string{`<p><img src="image.png" alt="Synthetic color study" /></p>`, `<p><img src="https://images.example.invalid/image.png" alt="Synthetic" /></p>`, "<p>First<br \n/>Second</p>"} {
		p, e := NewPost("test", CreatePost{Title: "Synthetic", HTML: source})
		if e != nil || p.HTML != source || p.SourceHTML != source {
			t.Fatal("safe iA spacing rejected or source changed", e)
		}
	}
	for _, source := range []string{`<p><img src="x" onerror="alert(1)" /></p>`, `<img src="javascript:alert(1)" />`, `<img src="file:///private/example.png" />`, `<img src="x" style="background:url(javascript:alert(1))" />`, `<script />`, `<div />`} {
		if _, e := NewPost("test", CreatePost{Title: "Synthetic", HTML: source}); e == nil {
			t.Fatal("unsafe or non-void sanitizer difference accepted", source)
		}
	}
}

func TestEscapingOnlyDifferencesKeepSource(t *testing.T) {
	for _, source := range []string{
		`<p>Don't say "never".</p>`,
		`<p>Non&nbsp;breaking and caf&eacute;</p>`,
		"<p>Non breaking</p>",
		`<p><a rel="nofollow" href="https://example.invalid/?a=1&amp;b=2">Reordered</a></p>`,
		`<p title="It's caf&eacute;">Attribute escaping</p>`,
		`<p>1 &lt; 2 &amp;&amp; 3 &gt; 2</p><p>a<br/>b</p>`,
		`<table><tbody><tr><th colspan="1" rowspan="1"><p>Head</p></th></tr></tbody></table>`,
	} {
		p, e := NewPost("test", CreatePost{Title: "Synthetic", HTML: source})
		if e != nil || p.SourceHTML != source || p.HTML != source {
			t.Fatalf("equivalent source rejected or rewritten: %q %v", source, e)
		}
	}
	for _, source := range []string{
		`<p><a href="https://example.invalid/" rel="nofollow" target="_blank">Target</a></p>`,
		`<p><a href="javascript:alert(1)" rel="nofollow">Script</a></p>`,
		`<p><a href="https://example.invalid/a b" rel="nofollow">Rewritten URL</a></p>`,
		`<p>Comment<!-- hidden --></p>`,
		`<p onclick="alert(1)">Handler</p>`,
		`<p>Text<script>alert(1)</script></p>`,
		`<p class="lead">Class</p>`,
		`<ol start="3"><li>Start</li></ol>`,
		`<div />`,
	} {
		if _, e := NewPost("test", CreatePost{Title: "Synthetic", HTML: source}); e == nil {
			t.Fatalf("content-changing sanitizer difference accepted: %q", source)
		}
	}
}
