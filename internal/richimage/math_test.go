package richimage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var supportedFormulas = []string{
	`x^2`, `x_1`, `x^{2}+y^{2}=z^{2}`, `a_{ij}`, `e^{-x^2}`,
	`\sum_{i=1}^{n} i = \frac{n(n+1)}{2}`,
	`\int_0^1 x\,dx = \frac12`,
	`\lim_{x\to 0}\frac{\sin x}{x}=1`,
	`\sqrt{b^2-4ac}`, `\sqrt[3]{x+1}`,
	`x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}`,
	`\left(\frac{a}{b}\right)^2`, `\left[ x \right]`, `\left\{ \frac{1}{2} \right.`,
	`\alpha+\beta\leq\gamma`, `\Delta x \neq 0`, `\mathbf{v}\cdot\mathbf{w}`,
	`\text{当 } x>0 \text{ 时}`, `\vec{a}+\hat{b}+\bar{c}+\tilde{d}+\dot{e}`,
	`\begin{pmatrix} 1 & 2 \\ 3 & 4 \end{pmatrix}`,
	`\begin{bmatrix} a & b \\ c & d \end{bmatrix}`,
	`f(x)=\begin{cases} x^2 & x\ge 0 \\ -x & x<0 \end{cases}`,
	`\begin{aligned} a &= b+c \\ d &= e \end{aligned}`,
	`\binom{n}{k}`, `A \subseteq B \Rightarrow x \in A`, `f'(x)`, `\mathbb{R}`, `\overline{AB}`, `\prod_{k=1}^n k`,
}

func TestParseMathStructure(t *testing.T) {
	seq, err := parseMath(`x^2_1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 1 {
		t.Fatalf("want one node, got %d", len(seq))
	}
	sc, ok := seq[0].(*mScripts)
	if !ok || sc.sup == nil || sc.sub == nil {
		t.Fatalf("want sup+sub on one base, got %#v", seq[0])
	}
	seq, err = parseMath(`-x`)
	if err != nil || len(seq) != 2 {
		t.Fatalf("unary minus parse: %v %v", seq, err)
	}
}

func TestParseMathErrors(t *testing.T) {
	for _, bad := range []string{`\unknowncmd`, `\frac{1}`, `{x`, `x}`, `\left( x`, `\begin{foo}x\end{foo}`, `\begin{matrix}1`, `\begin{matrix}1\end{pmatrix}`, `^`} {
		if _, err := parseMath(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestRenderMathFormulas(t *testing.T) {
	fonts, err := loadFonts(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	fs := newFaces(fonts)
	defer fs.close()
	budget := maxMathPixels
	for _, f := range supportedFormulas {
		if strings.Contains(f, "当") && !fonts.cjk {
			continue
		}
		for _, display := range []bool{false, true} {
			img, base, err := renderMath(fs, f, 32, display, lightTheme.fg, &budget)
			if err != nil {
				if fonts.math == nil && strings.Contains(err.Error(), "无法显示") {
					continue // needs STIX Two Math, which is not available offline
				}
				t.Errorf("%q (display=%v): %v", f, display, err)
				continue
			}
			if base <= 0 || base >= float64(img.Bounds().Dy()) {
				t.Errorf("%q: baseline %v outside image height %d", f, base, img.Bounds().Dy())
			}
			if inkPixels(img) == 0 {
				t.Errorf("%q: nothing drawn", f)
			}
		}
	}
}

func TestRenderMathGallery(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("## Gallery\n\n")
	for _, f := range supportedFormulas {
		sb.WriteString("`" + strings.ReplaceAll(f, "`", "") + "`\n\n$$" + f + "$$\n\n")
	}
	sb.WriteString("Inline: $x^2$, $\\frac{a}{b}$, $\\sum_{i=1}^n i$ and $\\sqrt{x}$ in a sentence.\n\n")
	sb.WriteString("Broken: $\\unknowncmd{x}$ stays as text.\n")
	data, err := Render(Request{Format: FormatMarkdown, Content: sb.String(), Width: 800})
	if errors.Is(err, ErrNoCJKFont) {
		t.Skip("no CJK font installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	saveIfRequested(t, "math-gallery", data)
}
