package richimage

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("invalid PNG: %v", err)
	}
	return img
}

// inkPixels counts pixels that differ from the corner (background) color.
func inkPixels(img image.Image) int {
	b := img.Bounds()
	br, bg, bb, _ := img.At(b.Min.X, b.Min.Y).RGBA()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r != br || g != bg || bl != bb {
				n++
			}
		}
	}
	return n
}

func saveIfRequested(t *testing.T, name string, data []byte) {
	if dir := os.Getenv("RICHIMAGE_DUMP"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtractMath(t *testing.T) {
	cases := []struct {
		in    string
		items []mathItem
		keep  string // substring that must survive untouched
	}{
		{"cost is $5 and $10", nil, "$5 and $10"},
		{"inline $x_1 * y_2$ here", []mathItem{{expr: "x_1 * y_2"}}, "inline "},
		{"`$a$` stays", nil, "`$a$`"},
		{"```\n$a$\n```\n", nil, "$a$"},
		{"$$\n\\frac{a}{b}\n$$", []mathItem{{expr: "\\frac{a}{b}", display: true}}, ""},
		{"escaped \\$a\\$ text", nil, "\\$a\\$"},
	}
	for _, tc := range cases {
		got, items := extractMath(tc.in)
		if len(items) != len(tc.items) {
			t.Fatalf("%q: got %d items, want %d (%q)", tc.in, len(items), len(tc.items), got)
		}
		for i := range items {
			if items[i] != tc.items[i] {
				t.Fatalf("%q: item %d = %+v, want %+v", tc.in, i, items[i], tc.items[i])
			}
		}
		if !strings.Contains(got, tc.keep) {
			t.Fatalf("%q: result %q lost %q", tc.in, got, tc.keep)
		}
	}
}

const sampleMarkdown = "# Title\n\nSome **bold** text with `code` and inline math $x^2+y^2=z^2$.\n\n" +
	"$$\\frac{a+b}{2} \\geq \\sqrt{ab}$$\n\n- item one\n- item two\n  1. nested\n\n> quote\n\n" +
	"| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(\"hi\")\n```\n"

func TestRenderMarkdown(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		data, err := Render(Request{Format: FormatMarkdown, Content: sampleMarkdown, Theme: theme})
		if err != nil {
			t.Fatalf("%s: %v", theme, err)
		}
		img := decodePNG(t, data)
		if img.Bounds().Dx() != DefaultWidth*scale {
			t.Fatalf("width = %d", img.Bounds().Dx())
		}
		if inkPixels(img) < 2000 {
			t.Fatalf("%s: image looks blank", theme)
		}
		saveIfRequested(t, "markdown-"+theme, data)
	}
}

func TestRenderMarkdownBadMathFallsBackToText(t *testing.T) {
	data, err := Render(Request{Format: FormatMarkdown, Content: "ok $\\begin{pmatrix}1\\end{pmatrix}$ end"})
	if err != nil {
		t.Fatalf("bad math must not fail the render: %v", err)
	}
	decodePNG(t, data)
}

func TestRenderMarkdownCJK(t *testing.T) {
	data, err := Render(Request{Format: FormatMarkdown, Content: "## 解方程\n\n已知 $x^2-5x+6=0$，则 $x=2$ 或 $x=3$。"})
	if errors.Is(err, ErrNoCJKFont) {
		t.Skip("no CJK font installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	saveIfRequested(t, "markdown-cjk", data)
	if inkPixels(decodePNG(t, data)) < 2000 {
		t.Fatal("image looks blank")
	}
}

func TestRenderMissingFontPathFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.otf")
	if _, err := Render(Request{Format: FormatMarkdown, Content: "你好", FontPath: path}); err == nil {
		t.Fatal("missing font path must fail")
	}
}

func TestRenderSVG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100">
<rect x="10" y="10" width="80" height="40" fill="#cde" stroke="#036"/>
<circle cx="150" cy="50" r="30" fill="orange"/>
<text x="50" y="35" font-size="14" text-anchor="middle" fill="#036">Input</text>
<g transform="translate(100,0)"><text x="50" y="90" font-size="12">Out</text></g>
</svg>`
	data, err := Render(Request{Format: FormatSVG, Content: svg})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, data)
	if img.Bounds().Dx() != DefaultWidth*scale || img.Bounds().Dy() != DefaultWidth*scale/2 {
		t.Fatalf("size = %v", img.Bounds())
	}
	if inkPixels(img) < 5000 {
		t.Fatal("svg looks blank")
	}
	saveIfRequested(t, "svg", data)
}

func TestRenderValidation(t *testing.T) {
	cases := map[string]Request{
		"empty":        {Format: FormatMarkdown, Content: "  "},
		"bad format":   {Format: "html", Content: "<b>x</b>"},
		"narrow":       {Format: FormatMarkdown, Content: "x", Width: 10},
		"wide":         {Format: FormatMarkdown, Content: "x", Width: 5000},
		"bad theme":    {Format: FormatMarkdown, Content: "x", Theme: "blue"},
		"huge md":      {Format: FormatMarkdown, Content: strings.Repeat("a ", MaxMarkdownBytes)},
		"svg no size":  {Format: FormatSVG, Content: "<svg xmlns='http://www.w3.org/2000/svg'></svg>"},
		"svg garbage":  {Format: FormatSVG, Content: "not xml at all"},
		"too tall md":  {Format: FormatMarkdown, Content: strings.Repeat("line\n\n", 2500)},
		"svg too tall": {Format: FormatSVG, Content: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 10 1000'></svg>"},
	}
	for name, req := range cases {
		if _, err := Render(req); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
