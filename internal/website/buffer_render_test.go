package website

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Read the actual rendered main buffer, retaining every text whitespace byte.
// Native field contents stay opaque; hidden anti-CSRF/revision fields add none.
func mainBufferText(t *testing.T, document []byte) string {
	t.Helper()
	root, err := html.Parse(bytes.NewReader(document))
	if err != nil {
		t.Fatal("SITE_TEST_BUFFER_PARSE")
	}
	var main *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "main" {
			main = n
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(root)
	if main == nil {
		t.Fatal("SITE_TEST_BUFFER_MAIN")
	}
	var text strings.Builder
	var read func(*html.Node)
	read = func(n *html.Node) {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "input" {
			for _, attr := range n.Attr {
				if attr.Key == "type" && attr.Val == "hidden" {
					return
				}
			}
			text.WriteString("[input]")
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			read(child)
		}
	}
	read(main)
	return text.String()
}

func TestBufferPagesHaveOnlyAuthoredWhitespace(t *testing.T) {
	r := prepared(t)
	for _, tc := range []struct{ entry, want string }{
		{"public-home", "Possums\nPrivate inference, with clear boundaries.\nSign in\n"},
		{"login-page", "Sign in\nKey: [input] Sign in\nSign-in requires first-party cookies. Tor New Identity or restarting your browser may end your session.\nBack to Possums\n"},
		{"signed-home", "Home\nYou are signed in.\nBalance: unavailable\nAvailable: unavailable\n\n\nSign out\n"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			p, err := r.Page(context.Background(), tc.entry)
			if err != nil {
				t.Fatal("SITE_TEST_BUFFER_PAGE")
			}
			b, err := r.Render(p, "synthetic-csrf", "")
			if err != nil || mainBufferText(t, b) != tc.want {
				t.Fatal("SITE_TEST_BUFFER_WHITESPACE")
			}
		})
	}
	css := r.CSS()
	for _, forbidden := range []string{"gap:", "display:flex", "display:grid", "min-height:", "--space", "clamp(", "calc(", "margin-block:", "padding-block:"} {
		if strings.Contains(css, forbidden) {
			t.Fatal("SITE_TEST_BUFFER_IMPLICIT_SPACING")
		}
	}
	for _, want := range []string{"white-space:pre-wrap", "tab-size:8", "body{margin:0;padding:0;padding-inline-start:1rem;padding-block-start:1rem}", "display:inline;font:inherit;margin:0;padding:0", "width:28ch", "vertical-align:baseline"} {
		if !strings.Contains(css, want) {
			t.Fatal("SITE_TEST_BUFFER_STYLE")
		}
	}
}

func TestNoticeUsesOnlyBundleAuthoredSuffix(t *testing.T) {
	b := Fixture()
	b["pages.el"] = strings.ReplaceAll(b["pages.el"], `(ui:notice "\n")`, `(ui:notice "\t  ")`)
	r, err := Prepare(context.Background(), b)
	if err != nil {
		t.Fatal("SITE_TEST_NOTICE_BUNDLE")
	}
	p, err := r.Page(context.Background(), "login-page")
	if err != nil {
		t.Fatal("SITE_TEST_NOTICE_PAGE")
	}
	out, err := r.Render(p, "", "Synthetic notice")
	if err != nil || !strings.HasPrefix(mainBufferText(t, out), "Sign in\nKey: [input] Sign in\nSynthetic notice\t  Sign-in requires") {
		t.Fatal("SITE_TEST_NOTICE_SUFFIX")
	}
}

func TestLiteralBufferWhitespaceIsEscapedAndBounded(t *testing.T) {
	b := Fixture()
	b["pages.el"] = strings.Replace(b["pages.el"], `(ui:p "Private inference, with clear boundaries.")`, `(ui:text "one\t two\n\nthree  four<script>&")`, 1)
	r, err := Prepare(context.Background(), b)
	if err != nil {
		t.Fatal("SITE_TEST_TEXT_CAPABILITY")
	}
	p, err := r.Page(context.Background(), "public-home")
	if err != nil {
		t.Fatal("SITE_TEST_TEXT_PAGE")
	}
	out, err := r.Render(p, "", "")
	if err != nil || strings.Contains(string(out), "<script>") || mainBufferText(t, out) != "Possums\none\t two\n\nthree  four<script>&\nSign in\n" {
		t.Fatal("SITE_TEST_TEXT_ESCAPE_WHITESPACE")
	}
	for _, node := range []Node{
		{Kind: "text", Text: strings.Repeat(" ", maxTextBytes)},
		{Kind: "text", Children: []Node{{Kind: "p", Text: "synthetic"}}},
	} {
		page := Node{Kind: "document", Text: "Bound", Children: []Node{{Kind: "main", Children: []Node{node}}}}
		if result, err := r.Render(page, "", ""); err == nil || result != nil {
			t.Fatal("SITE_TEST_TEXT_BOUND_REFUSAL")
		}
	}
	for _, expression := range []string{`(ui:text '(not a string))`, `(ui:text "one" "two")`} {
		candidate := Fixture()
		candidate["pages.el"] = strings.Replace(candidate["pages.el"], `(ui:p "Private inference, with clear boundaries.")`, expression, 1)
		if _, err := Prepare(context.Background(), candidate); err == nil {
			t.Fatal("SITE_TEST_TEXT_ADMISSION_REFUSAL")
		}
	}
}
