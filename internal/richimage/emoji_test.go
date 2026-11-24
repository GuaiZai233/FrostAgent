package richimage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Without the emoji font, an emoji must fail the render instead of silently
// drawing the body font's notdef box (the "?" square seen in production).
func TestEmojiWithoutFontFailsLoudly(t *testing.T) {
	old := fontDownloadEnabled
	fontDownloadEnabled = false
	t.Cleanup(func() { fontDownloadEnabled = old })
	_, err := Render(Request{Format: FormatMarkdown, Content: "验证 ✅ 通过", FontDir: t.TempDir()})
	if err == nil {
		t.Fatal("emoji without a font must not render as a notdef box")
	}
	if !strings.Contains(err.Error(), "✅") && !strings.Contains(err.Error(), "没有可用字形") {
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
