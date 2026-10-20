package website

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// spacingDeclarations captures margin/padding shorthand and longhand values.
var spacingDeclarations = regexp.MustCompile(`(?:^|[;{])(margin|padding)(?:-[a-z-]+)?:([^;}]+)`)

func TestRenderFormsEscapingAndStyle(t *testing.T) {
	r := prepared(t)
	p, err := r.Page(context.Background(), "login-page")
	if err != nil {
		t.Fatal("SITE_TEST_PAGE")
	}
	b, err := r.Render(p, "synthetic-csrf", `<script>hostile</script>`)
	if err != nil {
		t.Fatal("SITE_TEST_RENDER")
	}
	html := string(b)
	for _, want := range []string{`name="csrf_token" value="synthetic-csrf"`, `name="revision" value="` + r.Digest + `"`, `name="key"`, `autocomplete="current-password"`, `aria-describedby="form-error"`, `&lt;script&gt;hostile&lt;/script&gt;`, `<main id="main"`, `first-party cookies`, `method="post" action="/login"`} {
		if !strings.Contains(html, want) {
			t.Fatal("SITE_TEST_FORM_CONTRACT")
		}
	}
	// One-key sign-in: no identity selector and no identity/username autofill
	// hint may survive in the rendered form.
	for _, gone := range []string{`name="identity"`, `name="password"`, `autocomplete="username"`, `id="identity"`} {
		if strings.Contains(html, gone) {
			t.Fatal("SITE_TEST_LEGACY_CREDENTIAL_FIELD")
		}
	}
	for _, bad := range []string{`<script>`, `<style>`, `style=`, `onload=`, `localStorage`, `sessionStorage`} {
		if strings.Contains(html, bad) {
			t.Fatal("SITE_TEST_UNEXPECTED_ACTIVE_CONTENT")
		}
	}
	css := r.CSS()
	for _, want := range []string{"font-weight:400", "font-weight:700", "font-display:swap", "font-synthesis:none", "font-size:1rem", "border-radius:0", ":focus-visible", "forced-colors"} {
		if !strings.Contains(css, want) {
			t.Fatal("SITE_TEST_STYLE_CONTRACT")
		}
	}
	// Inter-node spacing is literal text-buffer whitespace, never CSS box spacing.
	// Only the outer body gutter may add padding; all buffer-internal box spacing
	// remains zero and no flex/grid gap may appear.
	if strings.Contains(css, "gap:") {
		t.Fatal("SITE_TEST_CSS_GAP_SPACING")
	}
	bufferCSS := strings.Replace(css, "body{margin:0;padding:0;padding-inline-start:1rem;padding-block-start:1rem}", "", 1)
	if bufferCSS == css {
		t.Fatal("SITE_TEST_BODY_GUTTER")
	}
	for _, declaration := range spacingDeclarations.FindAllStringSubmatch(bufferCSS, -1) {
		if strings.TrimSpace(declaration[2]) != "0" {
			t.Fatal("SITE_TEST_CSS_BOX_SPACING")
		}
	}
}

func TestTextNodeCarriesEscapedLiteralWhitespace(t *testing.T) {
	r := prepared(t)
	spacing := "\n\n"
	page := Node{Kind: "document", Text: "Spacing", Children: []Node{{Kind: "main", Children: []Node{
		{Kind: "h1", Text: "Heading"},
		{Kind: "text", Text: spacing},
		{Kind: "text", Text: "<b>&amp;</b>"},
		{Kind: "p", Text: "Body"},
	}}}}
	body, err := r.Render(page, "synthetic-csrf", "")
	if err != nil {
		t.Fatal("SITE_TEST_TEXT_NODE_RENDER")
	}
	html := string(body)
	if !strings.Contains(html, "<h1>Heading</h1>"+spacing+"&lt;b&gt;&amp;amp;&lt;/b&gt;<p>Body</p>") {
		t.Fatal("SITE_TEST_TEXT_NODE_WHITESPACE_OR_ESCAPING")
	}
	// A text node is literal data, never markup or an attribute carrier.
	for _, bad := range []string{"<b>", "&amp;</b>"} {
		if strings.Contains(html, bad) {
			t.Fatal("SITE_TEST_TEXT_NODE_RAW_MARKUP")
		}
	}
}

func TestRenderRefusesBeforeUnboundedAllocation(t *testing.T) {
	r := prepared(t)
	p, err := r.Page(context.Background(), "public-home")
	if err != nil {
		t.Fatal("SITE_TEST_RENDER_FIXTURE")
	}
	for _, page := range []Node{
		{Kind: "document", Children: []Node{{Kind: "main", Children: []Node{{Kind: "p", Text: strings.Repeat("<", maxTextBytes)}}}}},
		{Kind: "document", Children: []Node{{Kind: "main", Children: []Node{{Kind: "link", Target: "https://invalid.example", Text: "x"}}}}},
		{Kind: "document", Children: []Node{{Kind: "main", Children: []Node{{Kind: "script"}}}}},
		{Kind: "document", Children: []Node{{Kind: "group"}}},
	} {
		if b, err := r.Render(page, "", ""); err == nil || b != nil {
			t.Fatal("SITE_TEST_RENDER_BOUND_REFUSAL")
		}
	}
	cycle := Node{Kind: "group", Children: make([]Node, 1)}
	cycle.Children[0] = cycle
	p.Children[0].Children = []Node{cycle}
	if b, err := r.Render(p, "", ""); err == nil || b != nil {
		t.Fatal("SITE_TEST_RENDER_CYCLE_REFUSAL")
	}
	p, _ = r.Page(context.Background(), "public-home")
	if b, err := r.Render(p, strings.Repeat("x", 1025), ""); err == nil || b != nil {
		t.Fatal("SITE_TEST_RENDER_SLOT_BOUND")
	}
	var output documentBuffer
	if n, err := output.Write(make([]byte, maxDocumentBytes)); err != nil || n != maxDocumentBytes {
		t.Fatal("SITE_TEST_OUTPUT_LIMIT_EXACT")
	}
	if n, err := output.Write([]byte{0}); err == nil || n != 0 || output.Len() != maxDocumentBytes {
		t.Fatal("SITE_TEST_OUTPUT_LIMIT_GROWTH")
	}
	if _, err := r.Render(p, "synthetic-csrf", ""); err != nil {
		t.Fatal("SITE_TEST_RENDER_HEALTH_AFTER_REFUSAL")
	}
}

func TestNamedRoutesAndDefaultOptionsAreBundleData(t *testing.T) {
	b := Fixture()
	b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/home"`, `"/welcome"`)
	b["options.el"] = strings.ReplaceAll(b["options.el"], "home-show-introduction true", "home-show-introduction false")
	r, err := Prepare(context.Background(), b)
	if err != nil {
		t.Fatal("SITE_TEST_UPDATED_BUNDLE")
	}
	route, ok := r.Named("signed-home")
	if !ok || route.Path != "/welcome" {
		t.Fatal("SITE_TEST_ROUTE_NOT_DATA")
	}
	p, err := r.Page(context.Background(), "signed-home")
	if err != nil {
		t.Fatal("SITE_TEST_DEFAULT_PAGE")
	}
	html, err := r.Render(p, "synthetic-csrf", "")
	if err != nil || strings.Contains(string(html), "You are signed in.") || !strings.Contains(string(html), "Sign out") {
		t.Fatal("SITE_TEST_DEFAULT_NOT_APPLIED")
	}
}
