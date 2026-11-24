package richimage

import (
	"bytes"
	"image"
	"strings"
	"testing"
)

// Finding 1: hostile input must produce an error or an image, never a panic.
func TestRenderHostilePlaceholdersAndAttributes(t *testing.T) {
	md := []string{
		"-1",
		"5 and 999999",
		"&#57344;-1&#57345;",
		"&#57344;0&#57345;",
		"$x$ 0",
		"-1\n\n$$y$$",
	}
	for _, c := range md {
		if _, err := Render(Request{Format: FormatMarkdown, Content: c}); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	svgs := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text x="" y=" ">a</text></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text x="NaN" y="Inf" dx="," dy="">a</text></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text><tspan x=" , ">a</tspan></text></svg>`,
	}
	for _, c := range svgs {
		if _, err := Render(Request{Format: FormatSVG, Content: c}); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
}

func TestPlaceholderLookalikesStayText(t *testing.T) {
	m := &mdRenderer{c: &canvas{fs: newFaces(mustFonts(t)), th: lightTheme, scale: 2, mathBudget: maxMathPixels}, math: []mathItem{{expr: "x"}}}
	atoms := m.textWithMath("a&#57344;0&#57345;b", true, style{}, 16)
	for _, a := range atoms {
		if a.img != nil {
			t.Fatal("decoded character references must not become formulas")
		}
	}
	atoms = m.textWithMath("a-1b", false, style{}, 16)
	for _, a := range atoms {
		if a.img != nil {
			t.Fatal("negative placeholder index must not become a formula")
		}
	}
}

func mustFonts(t *testing.T) *fontSet {
	t.Helper()
	f, err := loadFonts("")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Finding 2: formula bitmaps share a bounded budget, checked before allocation.
func TestMathPixelBudget(t *testing.T) {
	fs := newFaces(mustFonts(t))
	defer fs.close()
	expr := `\begin{matrix}` + strings.Repeat(`1 & `, 60) + `1 \\ ` + strings.Repeat(`1 \\ `, 40) + `1\end{matrix}`
	budget := 1 << 30
	img, _, err := renderMath(fs, expr, 32, false, lightTheme.fg, &budget)
	if err != nil {
		t.Fatal(err)
	}
	used := (1 << 30) - budget
	if used != img.Bounds().Dx()*img.Bounds().Dy() {
		t.Fatalf("budget charged %d, image has %d pixels", used, img.Bounds().Dx()*img.Bounds().Dy())
	}
	tiny := used - 1
	if _, _, err := renderMath(fs, expr, 32, false, lightTheme.fg, &tiny); err == nil {
		t.Fatal("formula over the remaining budget must be rejected")
	}
	if tiny != used-1 {
		t.Fatal("a rejected formula must not consume budget")
	}
}

func TestRepeatedFormulasStayWithinBudget(t *testing.T) {
	c := &canvas{fs: newFaces(mustFonts(t)), th: lightTheme, scale: 2, mathBudget: 50000}
	defer c.fs.close()
	ok, rejected := 0, 0
	for i := 0; i < 50; i++ {
		if _, _, err := c.mathImage(`\frac{a+b}{c+d}`, 32, false, lightTheme.fg); err != nil {
			rejected++
		} else {
			ok++
		}
	}
	if ok == 0 || rejected == 0 || c.mathBudget < 0 {
		t.Fatalf("ok=%d rejected=%d remaining=%d", ok, rejected, c.mathBudget)
	}
}

// Finding 3: every column must stay inside the PNG.
func TestWideTableFitsOrErrors(t *testing.T) {
	head := ""
	sep := ""
	row := ""
	for i := 0; i < 6; i++ {
		head += "| 非常非常长的列标题 header" + string(rune('A'+i)) + " "
		sep += "|---"
		row += "| cell" + string(rune('A'+i)) + " "
	}
	content := head + "|\n" + sep + "|\n" + row + "|\n"
	data, err := Render(Request{Format: FormatMarkdown, Content: content, Width: MinWidth})
	if err != nil {
		if !strings.Contains(err.Error(), "表格") {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	img := decodePNG(t, data)
	// The right border column must carry ink inside the canvas.
	right := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Max.X - 2*int(2*20); x < b.Max.X; x++ {
			if r, g, bl, _ := img.At(x, y).RGBA(); r != 0xffff || g != 0xffff || bl != 0xffff {
				right++
			}
		}
	}
	if right == 0 {
		t.Fatal("rightmost column is outside the image")
	}
}

func TestTableWidthsNeverExceedCanvas(t *testing.T) {
	for cols := 2; cols <= 12; cols++ {
		var head, sep, row strings.Builder
		for i := 0; i < cols; i++ {
			head.WriteString("| a very long heading here ")
			sep.WriteString("|---")
			row.WriteString("| x ")
		}
		content := head.String() + "|\n" + sep.String() + "|\n" + row.String() + "|\n"
		fs := newFaces(mustFonts(t))
		c := &canvas{fs: fs, th: lightTheme, scale: 2, mathBudget: maxMathPixels}
		if _, err := renderMarkdown(c, content, float64(MinWidth*2)); err != nil && !strings.Contains(err.Error(), "表格") {
			t.Errorf("%d cols: %v", cols, err)
		}
		fs.close()
	}
}

// Finding 4: spaces inside 	ext keep their place in the drawn sequence.
func renderBoxes(w, h int, parts ...any) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+1 < len(parts); i += 2 {
		parts[i].(mbox).draw(img, parts[i+1].(float64), float64(h)/2)
	}
	return img
}

func TestTextKeepsInternalSpaces(t *testing.T) {
	fs := newFaces(mustFonts(t))
	defer fs.close()
	m := &mathLayout{fs: fs, col: lightTheme.fg}
	whole := m.strBox("ab cd", fontRoman, 40)
	left := m.strBox("ab", fontRoman, 40)
	right := m.strBox("cd", fontRoman, 40)
	if m.err != nil {
		t.Fatal(m.err)
	}
	space := 40 * 0.28
	if diff := whole.w - (left.w + space + right.w); diff < -0.01 || diff > 0.01 {
		t.Fatalf("width %v, want %v", whole.w, left.w+space+right.w)
	}
	got := renderBoxes(300, 100, whole, 10.0)
	want := renderBoxes(300, 100, left, 10.0, right, 10.0+left.w+space)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Fatal("glyph positions differ: the space was not preserved in the drawing sequence")
	}
	// A run without spaces must not match the spaced rendering.
	packed := renderBoxes(300, 100, m.strBox("abcd", fontRoman, 40), 10.0)
	if bytes.Equal(packed.Pix, got.Pix) {
		t.Fatal("spaced and packed text must differ")
	}
}
