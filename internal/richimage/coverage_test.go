package richimage

import (
	"context"
	"strings"
	"testing"
)

// commonMathCommands is the coverage target for homework-style math. Every
// entry must parse and lay out; the test reports all gaps at once instead of
// one at a time.
var commonMathCommands = []string{
	// big operators and integrals
	`\sum_{i=1}^n i`, `\prod_{i=1}^n i`, `\coprod_i i`, `\int_0^1 f`, `\iint_D f`, `\iiint_V f`,
	`\oint_L f`, `\oiint_S f`, `\bigcup_i A_i`, `\bigcap_i A_i`, `\bigoplus_i A_i`, `\bigotimes_i A_i`,
	`\bigvee_i a`, `\bigwedge_i a`,
	// functions and limits
	`\lim_{x\to\infty} f`, `\sin x`, `\cos x`, `\tan x`, `\cot x`, `\sec x`, `\csc x`,
	`\arcsin x`, `\arccos x`, `\arctan x`, `\sinh x`, `\cosh x`, `\tanh x`,
	`\log x`, `\ln x`, `\lg x`, `\exp x`, `\max_i a`, `\min_i a`, `\sup_i a`, `\inf_i a`,
	`\det A`, `\dim V`, `\ker T`, `\arg z`, `\gcd(a,b)`, `\Pr(A)`,
	// roots, fractions, scripts
	`\sqrt{x}`, `\sqrt[n]{x}`, `\frac{1}{2}`, `\dfrac{1}{2}`, `\tfrac{1}{2}`, `\binom{n}{k}`,
	`x^{2}`, `x_{i}`, `x_i^2`, `{}_a^b X`, `f'(x)`, `f''(x)`, `x^\prime`,
	// delimiters, sizing
	`\left( x \right)`, `\left[ x \right]`, `\left\{ x \right\}`, `\left\langle x \right\rangle`,
	`\left| x \right|`, `\left\| x \right\|`, `\left. x \right|`, `\bigl( x \bigr)`, `\Bigl[ x \Bigr]`,
	`\biggl\{ x \biggr\}`, `\Biggl( x \Biggr)`, `\lvert x \rvert`, `\lVert x \rVert`, `\lfloor x \rfloor`,
	`\lceil x \rceil`, `|x|`, `\|x\|`, `\langle x, y \rangle`,
	// relations
	`a=b`, `a\ne b`, `a\neq b`, `a\le b`, `a\leq b`, `a\ge b`, `a\geq b`, `a\leqslant b`,
	`a\geqslant b`, `a<b`, `a>b`, `a\lt b`, `a\gt b`, `a\approx b`, `a\sim b`, `a\simeq b`,
	`a\equiv b`, `a\cong b`, `a\propto b`, `a\doteq b`, `a\perp b`, `a\parallel b`, `a\mid b`,
	`a\nmid b`, `a\ll b`, `a\gg b`, `a\in A`, `a\notin A`, `A\subset B`, `A\supset B`,
	`A\subseteq B`, `A\supseteq B`, `A\subsetneq B`, `A\cup B`, `A\cap B`, `A\setminus B`,
	`\forall x`, `\exists x`, `\nexists x`, `\neg p`, `p\land q`, `p\lor q`, `p\wedge q`, `p\vee q`,
	`p\implies q`, `p\impliedby q`, `p\iff q`, `\therefore x`, `\because x`,
	// arrows
	`a\to b`, `a\rightarrow b`, `a\leftarrow b`, `a\leftrightarrow b`, `a\Rightarrow b`,
	`a\Leftarrow b`, `a\Leftrightarrow b`, `a\mapsto b`, `a\longrightarrow b`, `a\longleftarrow b`,
	`a\Longrightarrow b`, `a\uparrow b`, `a\downarrow b`, `a\gets b`,
	// binary operators and misc symbols
	`a\pm b`, `a\mp b`, `a\times b`, `a\div b`, `a\cdot b`, `a\ast b`, `a\star b`, `a\circ b`,
	`a\bullet b`, `a\cap b`, `a\oplus b`, `a\otimes b`, `a\odot b`, `a\wedge b`, `a\vee b`,
	`\partial f`, `\nabla f`, `\infty`, `\angle A`, `\triangle ABC`, `\hbar`, `\ell`, `\Re z`, `\Im z`,
	`\aleph`, `\emptyset`, `\varnothing`, `\prime`, `\backslash`, `\surd`, `\checkmark`, `\square`,
	`\blacksquare`, `\qed`, `\degree`, `\neg`, `\top`, `\bot`, `\vdash`, `\models`,
	`\ldots`, `\cdots`, `\vdots`, `\ddots`, `\dots`,
	// accents and fonts
	`\vec{v}`, `\hat{x}`, `\bar{x}`, `\overline{AB}`, `\underline{x}`, `\tilde{x}`, `\dot{x}`,
	`\ddot{x}`, `\widehat{AB}`, `\widetilde{x}`, `\overrightarrow{AB}`,
	`\mathbf{v}`, `\boldsymbol{v}`, `\bm{v}`, `\mathrm{d}x`, `\mathit{x}`, `\mathbb{R}`,
	`\mathcal{L}`, `\mathscr{L}`, `\mathfrak{g}`, `\mathsf{x}`, `\text{if } x`, `\textbf{x}`,
	`\operatorname{Re}`, `\mbox{ok}`,
	// spacing and styles
	`a\,b`, `a\;b`, `a\:b`, `a\!b`, `a\quad b`, `a\qquad b`, `\displaystyle x`, `\textstyle x`,
	// matrices and cases
	`\begin{matrix}1&2\\3&4\end{matrix}`, `\begin{pmatrix}1\\2\end{pmatrix}`,
	`\begin{bmatrix}1&0\\0&1\end{bmatrix}`, `\begin{vmatrix}a&b\\c&d\end{vmatrix}`,
	`\begin{Vmatrix}a\end{Vmatrix}`, `\begin{Bmatrix}a\end{Bmatrix}`,
	`\begin{cases}x&x>0\\-x&x\le0\end{cases}`, `\begin{aligned}a&=b\\c&=d\end{aligned}`,
	`\begin{align*}a&=b\\c&=d\end{align*}`, `\begin{gather}a\\b\end{gather}`,
	`\begin{array}{cc}1&2\end{array}`, `\begin{smallmatrix}1\end{smallmatrix}`,
	// over and under braces, stacking
	`\underbrace{a+b}_{n}`, `\overbrace{a+b}^{n}`, `\stackrel{def}{=}`, `\overset{def}{=}`,
	`\underset{x}{y}`, `\xrightarrow{f}`, `\xleftarrow{g}`,
	`\not=`, `\not\in`, `\cancel{x}`,
	// integrals with limits as used in the cases from user screenshots
	`\int_0^{2\pi}-a^2\,dt`, `-2\pi a^2\ \checkmark`, `\iint_\Sigma \vec F\cdot\vec n\,dS`,
	`\oint_L P\,dx+Q\,dy`, `\sum_{n=0}^{\infty}\frac{x^n}{n!}`, `\lim_{n\to\infty}\left(1+\frac1n\right)^n=e`,
}

// TestCommonCommandsParse fails listing every command the parser rejects.
func TestCommonCommandsParse(t *testing.T) {
	var gaps []string
	for _, expr := range commonMathCommands {
		if _, err := parseMath(expr); err != nil {
			gaps = append(gaps, expr+"  ("+err.Error()+")")
		}
	}
	if len(gaps) > 0 {
		t.Fatalf("%d unsupported expressions:\n  %s", len(gaps), strings.Join(gaps, "\n  "))
	}
}

// TestCommonCommandsLayout lays every expression out with real fonts and
// reports glyphs that no font provides. It skips when STIX is unavailable.
func TestCommonCommandsLayout(t *testing.T) {
	fonts, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if fonts.math == nil {
		t.Skip("needs STIX Two Math (run with RICHIMAGE_ONLINE=1)")
	}
	fs := newFaces(fonts)
	defer fs.close()
	var gaps []string
	for _, expr := range commonMathCommands {
		budget := maxMathPixels
		if _, _, err := renderMath(fs, expr, 32, true, lightTheme.fg, &budget); err != nil {
			gaps = append(gaps, expr+"  ("+err.Error()+")")
		}
	}
	if len(gaps) > 0 {
		t.Fatalf("%d expressions do not lay out:\n  %s", len(gaps), strings.Join(gaps, "\n  "))
	}
}
