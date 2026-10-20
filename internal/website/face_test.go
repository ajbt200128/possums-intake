package website

import (
	"context"
	"strings"
	"testing"
)

func TestTooltipFaceIsVisibleEscapedAndClosed(t *testing.T) {
	b := Fixture()
	b["pages.el"] = strings.Replace(b["pages.el"], `(ui:p "Private inference, with clear boundaries.")`, `(ui:face 'tooltip "synthetic <script>& note")`, 1)
	r, err := Prepare(context.Background(), b)
	if err != nil {
		t.Fatal("SITE_TEST_FACE_ADMISSION")
	}
	page, err := r.Page(context.Background(), "public-home")
	if err != nil {
		t.Fatal("SITE_TEST_FACE_PAGE")
	}
	out, err := r.Render(page, "", "")
	if err != nil || !strings.Contains(string(out), `<span class="face-tooltip">synthetic &lt;script&gt;&amp; note</span>`) || mainBufferText(t, out) != "Possums\nsynthetic <script>& note\nSign in\n" {
		t.Fatal("SITE_TEST_FACE_ESCAPE_VISIBLE_TEXT")
	}
	for _, expression := range []string{
		`(ui:face 'unknown "text")`,
		`(ui:face "tooltip" "text")`,
		`(ui:face 'tooltip '(not text))`,
		`(ui:face 'tooltip "text" "extra")`,
	} {
		candidate := Fixture()
		candidate["pages.el"] = strings.Replace(candidate["pages.el"], `(ui:p "Private inference, with clear boundaries.")`, expression, 1)
		if _, err := Prepare(context.Background(), candidate); err == nil {
			t.Fatal("SITE_TEST_FACE_REFUSAL")
		}
	}
	for _, node := range []Node{
		{Kind: "tooltip", Text: strings.Repeat("x", maxTextBytes)},
		{Kind: "tooltip", Children: []Node{{Kind: "text", Text: "synthetic"}}},
	} {
		page := Node{Kind: "document", Text: "Face", Children: []Node{{Kind: "main", Children: []Node{node}}}}
		if result, err := r.Render(page, "", ""); err == nil || result != nil {
			t.Fatal("SITE_TEST_FACE_BOUNDED_LEAF")
		}
	}
}

func TestTooltipAndInteractiveHighlightStyles(t *testing.T) {
	r := prepared(t)
	page, err := r.Page(context.Background(), "login-page")
	if err != nil {
		t.Fatal("SITE_TEST_TOOLTIP_PAGE")
	}
	out, err := r.Render(page, "", "")
	if err != nil || !strings.Contains(string(out), `<span class="face-tooltip">Sign-in requires first-party cookies.`) {
		t.Fatal("SITE_TEST_TOOLTIP_NOTE")
	}
	css := r.CSS()
	for _, want := range []string{
		".face-tooltip{font-style:italic;font-synthesis:style}",
		"input{color:inherit;background:color-mix(in srgb,currentColor 10%,transparent);border-color:transparent}",
		"input:focus{border-color:" + r.Style.Border + ";outline:none}",
		"input{color:FieldText;background:Field;border-color:transparent}",
		"input:focus{border-color:Highlight;outline:2px solid Highlight;outline-offset:1px}",
		"a:hover,button:hover,a:focus-visible,button:focus-visible{color:" + r.Style.Paper + ";background:" + r.Style.Link + "}",
		"button:hover,button:focus-visible{border-color:transparent}",
		"a:hover,button:hover,a:focus-visible,button:focus-visible{color:HighlightText;background:Highlight}",
	} {
		if !strings.Contains(css, want) {
			t.Fatal("SITE_TEST_FACE_HIGHLIGHT_STYLE")
		}
	}
}
