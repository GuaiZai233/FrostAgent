package richimage

import (
	"context"
	"testing"
)

func TestEscapedSpaceInlineMath(t *testing.T) {
	for _, in := range []string{`答案 $\ 4\pi R^3\ $。`, `$x\ $`} {
		_, items := extractMath(in)
		if len(items) != 1 {
			t.Errorf("%q: want one formula, got %d", in, len(items))
		}
	}
	for _, in := range []string{`$ x$`, `$x $`, `costs $5 and $ 6`} {
		if _, items := extractMath(in); len(items) != 0 {
			t.Errorf("%q: plain spaces inside dollars must not be math", in)
		}
	}
}

func TestBoxedAndCdot(t *testing.T) {
	fonts, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs := newFaces(fonts)
	defer fs.close()
	budget := maxMathPixels
	plain, _, err := renderMath(fs, `I=4\pi R^3`, 32, true, lightTheme.fg, &budget)
	if err != nil {
		t.Skipf("needs STIX Two Math: %v", err)
	}
	boxed, _, err := renderMath(fs, `\boxed{\ I=4\pi R^3\ }`, 32, true, lightTheme.fg, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if boxed.Bounds().Dx() <= plain.Bounds().Dx() || boxed.Bounds().Dy() <= plain.Bounds().Dy() {
		t.Fatal("boxed formula must be larger than its content")
	}
	if _, err := parseMath(`\vec{F}\cdot\vec{n}`); err != nil {
		t.Fatal(err)
	}
	if mathSymbols["cdot"].text != "⋅" {
		t.Fatalf("cdot must use U+22C5, got %q", mathSymbols["cdot"].text)
	}
}
