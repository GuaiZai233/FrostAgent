package richimage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/image/font"
)

// Without the emoji font, an emoji must fail the render instead of silently
// drawing the body font's notdef box (the "?" square seen in production).
func TestEmojiWithoutFontFailsLoudly(t *testing.T) {
	old := fontDownloadEnabled
	fontDownloadEnabled = false
	t.Cleanup(func() { fontDownloadEnabled = old })
	// Latin-only content: a CJK character would fail first on machines without
	// a body font, hiding the emoji error this test is about.
	_, err := Render(Request{Format: FormatMarkdown, Content: "ok ✅ done", FontDir: t.TempDir()})
	if err == nil {
		t.Fatal("emoji without a font must not render as a notdef box")
	}
	if !strings.Contains(err.Error(), "✅") {
		t.Fatalf("error should name the missing glyph: %v", err)
	}
}

func TestSplitRunsChoosesFontPerRune(t *testing.T) {
	set, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs := newFaces(set)
	defer fs.close()
	// Latin text stays on the body font; ASCII in code uses the mono font.
	runs, err := fs.splitRuns("abc", false)
	if err != nil || len(runs) != 1 || runs[0].font != set.text {
		t.Fatalf("plain Latin must use the body font: %+v %v", runs, err)
	}
	runs, err = fs.splitRuns("x=1", true)
	if err != nil || len(runs) != 1 || runs[0].font != set.mono {
		t.Fatalf("code ASCII must use the mono font: %+v %v", runs, err)
	}
	// Whitespace and control characters never fail.
	if _, err := fs.splitRuns(" \t\n", false); err != nil {
		t.Fatalf("whitespace must not fail: %v", err)
	}
}

func TestCJKStillReportsMissingFont(t *testing.T) {
	old := fontDownloadEnabled
	fontDownloadEnabled = false
	t.Cleanup(func() { fontDownloadEnabled = old })
	set, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if set.cjk {
		t.Skip("a system CJK font is installed on this machine")
	}
	if err := newFaces(set).checkText("中"); !errors.Is(err, ErrNoCJKFont) {
		t.Fatalf("CJK without a body font must report ErrNoCJKFont: %v", err)
	}
}

// Online only: the monochrome emoji outlines must actually be drawn.
func TestEmojiRendersWithPinnedFont(t *testing.T) {
	set, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if set.emoji == nil {
		t.Skip("needs Noto Emoji (run with RICHIMAGE_ONLINE=1)")
	}
	withEmoji, err := Render(Request{Format: FormatMarkdown, Content: "✅ ✔ ❌ ⚠ ⭐ 🔥", FontDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if inkPixels(decodePNG(t, withEmoji)) < 2000 {
		t.Fatal("emoji line looks blank")
	}
	saveIfRequested(t, "emoji", withEmoji)
}

// Code-style spaces and indentation must advance with the monospace face so
// columns line up with the monospace glyphs around them.
func TestCodeSpacesUseMonoFont(t *testing.T) {
	set, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs := newFaces(set)
	defer fs.close()
	for _, text := range []string{"x = 1", "    return a", "\tb"} {
		runs, err := fs.splitRuns(text, true)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		for _, r := range runs {
			if r.font != set.mono {
				t.Errorf("%q: run %q uses a non-mono font", text, r.text)
			}
		}
	}
	// A code-style space must measure exactly like the mono space advance.
	c := &canvas{fs: fs, th: lightTheme, scale: 2}
	gotSpace := c.measure(" ", style{code: true}, 16)
	face, err := fs.faceOf(set.mono, 16)
	if err != nil {
		t.Fatal(err)
	}
	wantSpace := float64(font.MeasureString(face, " ")) / 64
	if gotSpace != wantSpace {
		t.Fatalf("code space width %v, mono space advance %v", gotSpace, wantSpace)
	}
	// Non-ASCII whitespace still falls back to the body font without failing.
	if _, err := fs.splitRuns("a　b", true); err != nil {
		t.Fatalf("ideographic space must not fail: %v", err)
	}
}
