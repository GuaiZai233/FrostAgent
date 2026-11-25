package richimage

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

type theme struct {
	bg, fg, muted, border, codeBG, accent color.RGBA
	// syntax colours code tokens by class; synPlain uses fg.
	syntax [synClasses]color.RGBA
}

var lightTheme = theme{
	bg: color.RGBA{255, 255, 255, 255}, fg: color.RGBA{31, 35, 40, 255},
	muted: color.RGBA{89, 99, 110, 255}, border: color.RGBA{208, 215, 222, 255},
	codeBG: color.RGBA{246, 248, 250, 255}, accent: color.RGBA{9, 105, 218, 255},
	syntax: [synClasses]color.RGBA{
		synPlain:    {31, 35, 40, 255},
		synKeyword:  {207, 34, 46, 255},
		synString:   {10, 48, 105, 255},
		synComment:  {110, 119, 129, 255},
		synNumber:   {5, 80, 174, 255},
		synFunction: {130, 80, 223, 255},
		synBuiltin:  {149, 56, 0, 255},
		synOperator: {207, 34, 46, 255},
	},
}

var darkTheme = theme{
	bg: color.RGBA{13, 17, 23, 255}, fg: color.RGBA{230, 237, 243, 255},
	muted: color.RGBA{145, 152, 161, 255}, border: color.RGBA{48, 54, 61, 255},
	codeBG: color.RGBA{22, 27, 34, 255}, accent: color.RGBA{88, 166, 255, 255},
	syntax: [synClasses]color.RGBA{
		synPlain:    {230, 237, 243, 255},
		synKeyword:  {255, 123, 114, 255},
		synString:   {165, 214, 255, 255},
		synComment:  {139, 148, 158, 255},
		synNumber:   {121, 192, 255, 255},
		synFunction: {210, 168, 255, 255},
		synBuiltin:  {255, 166, 87, 255},
		synOperator: {255, 123, 114, 255},
	},
}

type style struct {
	bold, code, link, strike bool
}

// atom is the smallest unbreakable inline unit.
type atom struct {
	text  string
	st    style
	size  float64
	w     float64
	space bool
	br    bool
	img   *image.RGBA
	ibase float64 // distance from the image top to its text baseline
	col   *color.RGBA
}

type rect struct {
	x, y, w, h float64
	col        color.RGBA
	set        bool
}

// canvas collects layout operations; drawing happens once the height is known.
type canvas struct {
	// ctx is the render's context; cancellable waits check it.
	ctx   context.Context
	fs    *faces
	th    theme
	scale float64
	ops   []func(*image.RGBA)
	err   error
	// mathBudget is the remaining pixel count formula images may allocate.
	mathBudget int
}

func (c *canvas) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

type run struct {
	text string
	font *sfnt.Font
}

// splitRuns assigns every rune to the font that draws it, grouping neighbours
// that share a font. Whitespace and control characters never fail.
func (fs *faces) splitRuns(text string, mono bool) ([]run, error) {
	var out []run
	var cur []rune
	var curFont *sfnt.Font
	var firstErr error
	flush := func() {
		if len(cur) > 0 {
			out = append(out, run{string(cur), curFont})
			cur = cur[:0]
		}
	}
	for _, r := range text {
		var f *sfnt.Font
		switch {
		case mono && r < 0x80:
			// ASCII in code style, spaces included, keeps the monospace advance.
			f = fs.set.mono
		case unicode.IsSpace(r) || unicode.IsControl(r):
			f = fs.set.text
		default:
			g, ok := fs.glyphFont(r)
			if !ok {
				if firstErr == nil {
					firstErr = fs.missingGlyph(r)
				}
				g = fs.set.text
			}
			f = g
		}
		if f != curFont {
			flush()
			curFont = f
		}
		cur = append(cur, r)
	}
	flush()
	return out, firstErr
}

func (c *canvas) measure(text string, st style, size float64) float64 {
	total := 0.0
	runs, err := c.fs.splitRuns(text, st.code)
	c.fail(err)
	for _, r := range runs {
		face, err := c.fs.faceOf(r.font, size)
		if err != nil {
			c.fail(err)
			return 0
		}
		total += float64(font.MeasureString(face, r.text)) / 64
	}
	if st.bold {
		total += size / 24
	}
	return total
}

func (c *canvas) drawText(dst *image.RGBA, x, baseline float64, text string, st style, size float64, col color.RGBA) {
	runs, err := c.fs.splitRuns(text, st.code)
	c.fail(err)
	for _, r := range runs {
		face, err := c.fs.faceOf(r.font, size)
		if err != nil {
			c.fail(err)
			return
		}
		d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face,
			Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}}
		d.DrawString(r.text)
		end := float64(d.Dot.X) / 64
		if st.bold {
			d.Dot.X = fixed.Int26_6((x + size/24) * 64)
			d.DrawString(r.text)
		}
		x = end
	}
}

func fillRect(dst *image.RGBA, x, y, w, h float64, col color.RGBA) {
	r := image.Rect(int(x+0.5), int(y+0.5), int(x+w+0.5), int(y+h+0.5)).Intersect(dst.Bounds())
	if !r.Empty() {
		draw.Draw(dst, r, image.NewUniform(col), image.Point{}, draw.Over)
	}
}

func (c *canvas) deferRect() *rect {
	rc := &rect{}
	c.ops = append(c.ops, func(d *image.RGBA) {
		if rc.set {
			fillRect(d, rc.x, rc.y, rc.w, rc.h, rc.col)
		}
	})
	return rc
}

func (c *canvas) hline(x, y, w, thick float64, col color.RGBA) {
	c.ops = append(c.ops, func(d *image.RGBA) { fillRect(d, x, y, w, thick, col) })
}

func isCJK(r rune) bool { return r >= 0x2E80 }

// textAtoms splits text into spaces, CJK runes and Latin words.
func (c *canvas) textAtoms(text string, st style, size float64) []atom {
	var out []atom
	if c.err == nil {
		c.err = c.fs.checkText(text)
	}
	var word []rune
	flush := func() {
		if len(word) > 0 {
			s := string(word)
			out = append(out, atom{text: s, st: st, size: size, w: c.measure(s, st, size)})
			word = word[:0]
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			flush()
			out = append(out, atom{text: " ", st: st, size: size, w: c.measure(" ", st, size), space: true})
		case isCJK(r):
			flush()
			s := string(r)
			out = append(out, atom{text: s, st: st, size: size, w: c.measure(s, st, size)})
		default:
			word = append(word, r)
		}
	}
	flush()
	return out
}

// mathImage renders a LaTeX math expression as a transparent image and returns
// the baseline offset from its top.
func (c *canvas) mathImage(expr string, size float64, display bool, col color.RGBA) (img *image.RGBA, base float64, err error) {
	if len(expr) > 600 {
		return nil, 0, fmt.Errorf("公式过长")
	}
	defer func() {
		if r := recover(); r != nil {
			img, base, err = nil, 0, fmt.Errorf("公式无法渲染: %v", r)
		}
	}()
	return renderMath(c.fs, expr, size, display, col, &c.mathBudget)
}

// flow lays out atoms inside [x, x+maxW) starting at y and returns the height used.
// align: 0 left, 1 center.
func (c *canvas) flow(atoms []atom, x, y, maxW float64, align int) float64 {
	// Break overlong words into runes and shrink overwide images.
	var work []atom
	for _, a := range atoms {
		switch {
		case a.img != nil && float64(a.img.Bounds().Dx()) > maxW:
			ratio := maxW / float64(a.img.Bounds().Dx())
			nb := image.Rect(0, 0, int(maxW), int(float64(a.img.Bounds().Dy())*ratio)+1)
			scaled := image.NewRGBA(nb)
			xdraw.ApproxBiLinear.Scale(scaled, nb, a.img, a.img.Bounds(), xdraw.Over, nil)
			a.img, a.w, a.ibase = scaled, maxW, a.ibase*ratio
			work = append(work, a)
		case a.img == nil && !a.br && a.w > maxW && len([]rune(a.text)) > 1:
			for _, r := range a.text {
				s := string(r)
				work = append(work, atom{text: s, st: a.st, size: a.size, w: c.measure(s, a.st, a.size), col: a.col})
			}
		default:
			work = append(work, a)
		}
	}
	top := y
	var line []atom
	lw := 0.0
	flush := func() {
		for len(line) > 0 && line[len(line)-1].space {
			lw -= line[len(line)-1].w
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			return
		}
		maxSize, asc, desc := 0.0, 0.0, 0.0
		for _, a := range line {
			if a.size > maxSize {
				maxSize = a.size
			}
		}
		for _, a := range line {
			if a.img != nil {
				h := float64(a.img.Bounds().Dy())
				asc = maxFloat(asc, a.ibase)
				desc = maxFloat(desc, h-a.ibase)
			} else {
				asc = maxFloat(asc, a.size*0.86)
				desc = maxFloat(desc, a.size*0.14)
			}
		}
		lead := maxSize * 0.55
		baseline := top + lead/2 + asc
		cx := x
		if align == 1 {
			cx = x + (maxW-lw)/2
		}
		for _, a := range line {
			a, ax := a, cx
			col := c.th.fg
			if a.col != nil {
				col = *a.col
			}
			if a.st.link {
				col = c.th.accent
			}
			cx += a.w
			if a.img != nil {
				iy := baseline - a.ibase
				c.ops = append(c.ops, func(d *image.RGBA) {
					r := image.Rect(int(ax), int(iy), int(ax)+a.img.Bounds().Dx(), int(iy)+a.img.Bounds().Dy())
					draw.Draw(d, r, a.img, a.img.Bounds().Min, draw.Over)
				})
				continue
			}
			if a.st.code {
				bgY, bgH := baseline-a.size*0.95, a.size*1.25
				c.ops = append(c.ops, func(d *image.RGBA) { fillRect(d, ax, bgY, a.w, bgH, c.th.codeBG) })
			}
			if a.space && !a.st.code {
				continue
			}
			bl := baseline
			c.ops = append(c.ops, func(d *image.RGBA) {
				c.drawText(d, ax, bl, a.text, a.st, a.size, col)
				if a.st.strike {
					fillRect(d, ax, bl-a.size*0.3, a.w, maxFloat(1, a.size/16), col)
				}
			})
		}
		top += asc + desc + lead
		line, lw = nil, 0
	}
	for _, a := range work {
		if a.br {
			if len(line) == 0 {
				top += 16 * c.scale * 0.8
			}
			flush()
			continue
		}
		if a.space && len(line) == 0 {
			continue
		}
		if lw+a.w > maxW && len(line) > 0 && !a.space {
			flush()
		}
		line = append(line, a)
		lw += a.w
	}
	flush()
	return top - y
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// finish draws all collected operations on a new image.
func (c *canvas) finish(width int, height float64) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, int(height)+1))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(c.th.bg), image.Point{}, draw.Src)
	for _, op := range c.ops {
		op(dst)
	}
	return dst
}
