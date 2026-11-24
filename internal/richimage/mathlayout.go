package richimage

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

type mbox struct {
	w, asc, desc float64
	draw         func(dst *image.RGBA, x, base float64)
}

type mstyle struct {
	size    float64
	root    float64
	display bool
}

func (s mstyle) script() mstyle {
	n := s.size * 0.72
	if n < s.root*0.5 {
		n = s.root * 0.5
	}
	return mstyle{size: n, root: s.root}
}

type mathLayout struct {
	fs  *faces
	col color.RGBA
	err error
}

func (m *mathLayout) fail(format string, args ...any) {
	if m.err == nil {
		m.err = fmt.Errorf(format, args...)
	}
}

// renderMath draws expr to a transparent image and returns the baseline offset
// from the image top.
func renderMath(fs *faces, expr string, size float64, display bool, col color.RGBA) (*image.RGBA, float64, error) {
	seq, err := parseMath(expr)
	if err != nil {
		return nil, 0, err
	}
	m := &mathLayout{fs: fs, col: col}
	box := m.lay(seq, mstyle{size: size, root: size, display: display})
	if m.err != nil {
		return nil, 0, m.err
	}
	pad := size * 0.1
	w := int(math.Ceil(box.w + 2*pad))
	h := int(math.Ceil(box.asc + box.desc + 2*pad))
	if w <= 0 || h <= 0 || w > 8000 || h > 4000 {
		return nil, 0, fmt.Errorf("公式尺寸过大")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := box.asc + pad
	box.draw(img, pad, base)
	return img, base, nil
}

func (m *mathLayout) lay(n mnode, st mstyle) mbox {
	switch n := n.(type) {
	case nil:
		return mbox{draw: func(*image.RGBA, float64, float64) {}}
	case mseq:
		return m.laySeq(n, st)
	case *mAtom:
		return m.layAtom(n, st)
	case *mText:
		return m.strBox(n.text, fontRoman, st.size)
	case *mStyled:
		return m.layStyled(n, st)
	case *mSpace:
		return mbox{w: n.em * st.size, draw: func(*image.RGBA, float64, float64) {}}
	case *mScripts:
		return m.layScripts(n, st)
	case *mFrac:
		return m.layFrac(n, st)
	case *mSqrt:
		return m.laySqrt(n, st)
	case *mDelim:
		return m.layDelim(n, st)
	case *mAccent:
		return m.layAccent(n, st)
	case *mMatrix:
		return m.layMatrix(n, st)
	}
	m.fail("内部错误: 未知节点 %T", n)
	return mbox{draw: func(*image.RGBA, float64, float64) {}}
}

func (m *mathLayout) layStyled(n *mStyled, st mstyle) mbox {
	// Bold applies to plain atoms only; nested structure keeps its own fonts.
	var restyle func(x mnode) mnode
	restyle = func(x mnode) mnode {
		switch v := x.(type) {
		case *mAtom:
			c := *v
			if c.font == fontItalic {
				c.font = n.font
			}
			return &c
		case mseq:
			out := make(mseq, len(v))
			for i := range v {
				out[i] = restyle(v[i])
			}
			return out
		}
		return x
	}
	return m.lay(restyle(n.body), st)
}

func kindOf(n mnode) int {
	switch v := n.(type) {
	case *mAtom:
		return v.kind
	case *mScripts:
		return kindOf(v.base)
	}
	return kOrd
}

func (m *mathLayout) space(prev, cur int, st mstyle) float64 {
	if st.size < st.root*0.9 {
		return 0
	}
	switch {
	case prev == kRel || cur == kRel:
		return 0.28 * st.size
	case prev == kBin || cur == kBin:
		return 0.22 * st.size
	case prev == kPunct:
		return 0.17 * st.size
	case (prev == kOp && cur != kClose && cur != kPunct) || (cur == kOp && prev != kOpen && prev != kPunct):
		return 0.17 * st.size
	}
	return 0
}

func (m *mathLayout) laySeq(s mseq, st mstyle) mbox {
	type item struct {
		box mbox
		x   float64
	}
	items := make([]item, 0, len(s))
	kinds := make([]int, len(s))
	for i, n := range s {
		kinds[i] = kindOf(n)
	}
	// A binary operator at the start or after another operator is unary.
	for i, k := range kinds {
		if k != kBin {
			continue
		}
		if i == 0 || i == len(s)-1 {
			kinds[i] = kOrd
			continue
		}
		switch kinds[i-1] {
		case kBin, kRel, kOpen, kPunct, kOp:
			kinds[i] = kOrd
		}
	}
	x := 0.0
	var out mbox
	prev := -1
	for i, n := range s {
		b := m.lay(n, st)
		if prev >= 0 {
			x += m.space(prev, kinds[i], st)
		}
		items = append(items, item{b, x})
		x += b.w
		out.asc = math.Max(out.asc, b.asc)
		out.desc = math.Max(out.desc, b.desc)
		prev = kinds[i]
	}
	out.w = x
	out.draw = func(dst *image.RGBA, ox, base float64) {
		for _, it := range items {
			it.box.draw(dst, ox+it.x, base)
		}
	}
	return out
}

func (m *mathLayout) pick(sel int, r rune) *sfnt.Font {
	set := m.fs.set
	var order []*sfnt.Font
	switch sel {
	case fontItalic:
		order = []*sfnt.Font{set.goIt, set.text, set.goReg, set.math}
	case fontBold:
		order = []*sfnt.Font{set.goBold, set.text, set.goReg, set.math}
	default:
		order = []*sfnt.Font{set.text, set.goReg, set.math}
	}
	for _, f := range order {
		if hasGlyph(f, r) {
			return f
		}
	}
	return nil
}

// strBox lays out text, choosing a font per rune so missing symbols fall back.
func (m *mathLayout) strBox(text string, sel int, size float64) mbox {
	type seg struct {
		f    *sfnt.Font
		text string
		x    float64
	}
	var segs []seg
	var out mbox
	for _, r := range text {
		if r == ' ' {
			out.w += size * 0.28
			continue
		}
		f := m.pick(sel, r)
		if f == nil {
			m.fail("符号 %q 无法显示", string(r))
			continue
		}
		face, err := m.fs.faceOf(f, size)
		if err != nil {
			m.fail("%v", err)
			continue
		}
		bounds, adv, _ := face.GlyphBounds(r)
		if len(segs) > 0 && segs[len(segs)-1].f == f {
			segs[len(segs)-1].text += string(r)
		} else {
			segs = append(segs, seg{f: f, text: string(r), x: out.w})
		}
		out.w += float64(adv) / 64
		out.asc = math.Max(out.asc, -float64(bounds.Min.Y)/64)
		out.desc = math.Max(out.desc, float64(bounds.Max.Y)/64)
	}
	col := m.col
	fs := m.fs
	out.draw = func(dst *image.RGBA, x, base float64) {
		for _, sg := range segs {
			face, err := fs.faceOf(sg.f, size)
			if err != nil {
				return
			}
			d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face,
				Dot: fixed.Point26_6{X: fixed.Int26_6((x + sg.x) * 64), Y: fixed.Int26_6(base * 64)}}
			d.DrawString(sg.text)
		}
	}
	return out
}

func (m *mathLayout) layAtom(a *mAtom, st mstyle) mbox {
	if !a.big {
		b := m.strBox(a.text, a.font, st.size)
		if a.kind == kOp {
			b.w += st.size * 0.05
		}
		return b
	}
	// Large operators are enlarged and centered on the math axis.
	factor := 1.15
	if st.display {
		factor = 1.55
		if a.text == "∫" || a.text == "∬" || a.text == "∭" || a.text == "∮" {
			factor = 1.9
		}
	}
	inner := m.strBox(a.text, a.font, st.size*factor)
	shift := 0.25*st.size - (inner.asc-inner.desc)/2 // glyph baseline relative to the line baseline (up)
	return mbox{w: inner.w + st.size*0.05, asc: inner.asc + shift, desc: inner.desc - shift,
		draw: func(dst *image.RGBA, x, base float64) { inner.draw(dst, x, base-shift) }}
}

func (m *mathLayout) layScripts(n *mScripts, st mstyle) mbox {
	base := m.lay(n.base, st)
	ss := st.script()
	var sup, sub mbox
	hasSup, hasSub := n.sup != nil, n.sub != nil
	if hasSup {
		sup = m.lay(n.sup, ss)
	}
	if hasSub {
		sub = m.lay(n.sub, ss)
	}
	if a, ok := n.base.(*mAtom); ok && a.limits && st.display {
		w := math.Max(base.w, math.Max(sup.w, sub.w))
		out := mbox{w: w, asc: base.asc, desc: base.desc}
		supUp, subDown := 0.0, 0.0
		if hasSup {
			supUp = base.asc + 0.1*st.size + sup.desc
			out.asc = supUp + sup.asc
		}
		if hasSub {
			subDown = base.desc + 0.1*st.size + sub.asc
			out.desc = subDown + sub.desc
		}
		out.draw = func(dst *image.RGBA, x, b float64) {
			base.draw(dst, x+(w-base.w)/2, b)
			if hasSup {
				sup.draw(dst, x+(w-sup.w)/2, b-supUp)
			}
			if hasSub {
				sub.draw(dst, x+(w-sub.w)/2, b+subDown)
			}
		}
		return out
	}
	s := st.size
	// TeX-style shifts (sup1, sup_drop, sub1, sub_drop approximations).
	up := math.Max(base.asc-0.386*ss.size, 0.413*s)
	down := math.Max(base.desc+0.05*ss.size, 0.15*s)
	if hasSup && hasSub {
		gap := (up - sup.desc) - (sub.asc - down)
		if min := 0.16 * s; gap < min {
			down += min - gap
		}
	}
	if !hasSup {
		down = math.Max(down, sub.asc-0.35*s)
	}
	italic := 0.04 * s
	w := base.w + italic + math.Max(sup.w, sub.w)
	out := mbox{w: w, asc: base.asc, desc: base.desc}
	if hasSup {
		out.asc = math.Max(out.asc, up+sup.asc)
	}
	if hasSub {
		out.desc = math.Max(out.desc, down+sub.desc)
	}
	out.draw = func(dst *image.RGBA, x, b float64) {
		base.draw(dst, x, b)
		if hasSup {
			sup.draw(dst, x+base.w+italic, b-up)
		}
		if hasSub {
			sub.draw(dst, x+base.w+italic, b+down)
		}
	}
	return out
}

func (m *mathLayout) layFrac(n *mFrac, st mstyle) mbox {
	cs := st
	if !st.display {
		cs = st.script()
	}
	cs.display = false
	num, den := m.lay(n.num, cs), m.lay(n.den, cs)
	s := st.size
	axis := 0.25 * s
	bar := math.Max(1.5, 0.05*s)
	if !n.bar {
		bar = 0
	}
	gap := 0.12 * s
	w := math.Max(num.w, den.w) + 0.2*s
	numUp := axis + bar/2 + gap + num.desc
	denDown := den.asc + gap + bar/2 - axis
	box := mbox{w: w, asc: numUp + num.asc, desc: denDown + den.desc}
	col := m.col
	box.draw = func(dst *image.RGBA, x, b float64) {
		num.draw(dst, x+(w-num.w)/2, b-numUp)
		den.draw(dst, x+(w-den.w)/2, b+denDown)
		if bar > 0 {
			strokeSeg(dst, x+0.05*s, b-axis, x+w-0.05*s, b-axis, bar, col)
		}
	}
	if n.delims {
		return m.wrapDelims(box, "(", ")", st)
	}
	return box
}

func (m *mathLayout) laySqrt(n *mSqrt, st mstyle) mbox {
	body := m.lay(n.body, st)
	s := st.size
	gap := 0.12 * s
	th := math.Max(1.5, 0.05*s)
	rw := 0.62 * s
	top := body.asc + gap + th
	var idx mbox
	idxW := 0.0
	if n.index != nil {
		idx = m.lay(n.index, mstyle{size: st.root * 0.5, root: st.root})
		idxW = math.Max(0, idx.w-0.3*rw)
	}
	box := mbox{w: idxW + rw + body.w + 0.1*s, asc: top + th/2, desc: body.desc}
	col := m.col
	box.draw = func(dst *image.RGBA, x, b float64) {
		x0 := x + idxW
		yTop := b - top
		yBot := b + body.desc
		h := yBot - yTop
		strokeSeg(dst, x0, yBot-0.42*h, x0+0.3*rw, yBot, th*0.9, col)
		strokeSeg(dst, x0+0.3*rw, yBot, x0+rw, yTop, th*0.8, col)
		strokeSeg(dst, x0+rw-th*0.4, yTop, x0+rw+body.w+0.1*s, yTop, th, col)
		body.draw(dst, x0+rw, b)
		if n.index != nil {
			idx.draw(dst, x, b-top*0.55)
		}
	}
	return box
}

// wrapDelims puts stretchy delimiters around an already laid out box.
func (m *mathLayout) wrapDelims(body mbox, left, right string, st mstyle) mbox {
	s := st.size
	height := body.asc + body.desc
	scale := math.Max(1, height/(1.15*s))
	if scale > 3 {
		scale = 3
	}
	mk := func(d string) mbox {
		if d == "." || d == "" {
			return mbox{draw: func(*image.RGBA, float64, float64) {}}
		}
		return m.strBox(d, fontRoman, s*scale)
	}
	l, r := mk(left), mk(right)
	center := (body.asc - body.desc) / 2 // body center above the baseline
	align := func(b mbox) float64 { return center - (b.asc-b.desc)/2 }
	la, ra := align(l), align(r)
	pad := 0.05 * s
	box := mbox{w: l.w + body.w + r.w + 2*pad, asc: body.asc, desc: body.desc}
	box.asc = math.Max(box.asc, math.Max(l.asc+la, r.asc+ra))
	box.desc = math.Max(box.desc, math.Max(l.desc-la, r.desc-ra))
	box.draw = func(dst *image.RGBA, x, b float64) {
		l.draw(dst, x, b-la)
		body.draw(dst, x+l.w+pad, b)
		r.draw(dst, x+l.w+pad+body.w+pad, b-ra)
	}
	return box
}

func (m *mathLayout) layDelim(n *mDelim, st mstyle) mbox {
	return m.wrapDelims(m.lay(n.body, st), n.left, n.right, st)
}

func (m *mathLayout) layAccent(n *mAccent, st mstyle) mbox {
	body := m.lay(n.body, st)
	s := st.size
	th := math.Max(1.2, 0.05*s)
	lift := 0.22 * s
	box := mbox{w: body.w, asc: body.asc + lift, desc: body.desc}
	if n.kind == "underline" {
		box.asc, box.desc = body.asc, body.desc+lift
	}
	col := m.col
	box.draw = func(dst *image.RGBA, x, b float64) {
		body.draw(dst, x, b)
		y := b - body.asc - 0.1*s
		cx := x + body.w/2
		half := math.Min(body.w, 0.7*s) / 2
		switch n.kind {
		case "underline":
			strokeSeg(dst, x, b+body.desc+0.1*s, x+body.w, b+body.desc+0.1*s, th, col)
		case "bar", "overline":
			strokeSeg(dst, x, y, x+body.w, y, th, col)
		case "hat", "widehat":
			strokeSeg(dst, cx-half, y+0.1*s, cx, y-0.04*s, th, col)
			strokeSeg(dst, cx, y-0.04*s, cx+half, y+0.1*s, th, col)
		case "tilde", "widetilde":
			w := half * 2
			strokeSeg(dst, cx-half, y+0.06*s, cx-half+w/3, y-0.04*s, th, col)
			strokeSeg(dst, cx-half+w/3, y-0.04*s, cx-half+2*w/3, y+0.06*s, th, col)
			strokeSeg(dst, cx-half+2*w/3, y+0.06*s, cx+half, y-0.04*s, th, col)
		case "vec", "overrightarrow":
			x1 := x + body.w
			if n.kind == "vec" {
				x1 = cx + half
			}
			x0 := x1 - math.Max(body.w, 0.7*s)
			if n.kind == "vec" {
				x0 = cx - half
			}
			strokeSeg(dst, x0, y, x1, y, th, col)
			strokeSeg(dst, x1, y, x1-0.12*s, y-0.07*s, th, col)
			strokeSeg(dst, x1, y, x1-0.12*s, y+0.07*s, th, col)
		case "dot":
			strokeSeg(dst, cx-0.01*s, y, cx+0.01*s, y, 0.1*s, col)
		case "ddot":
			strokeSeg(dst, cx-0.13*s, y, cx-0.11*s, y, 0.1*s, col)
			strokeSeg(dst, cx+0.11*s, y, cx+0.13*s, y, 0.1*s, col)
		}
	}
	return box
}

func (m *mathLayout) layMatrix(n *mMatrix, st mstyle) mbox {
	cs := st
	cs.display = false
	s := st.size
	cols := 0
	for _, r := range n.rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	cells := make([][]mbox, len(n.rows))
	colW := make([]float64, cols)
	rowAsc := make([]float64, len(n.rows))
	rowDesc := make([]float64, len(n.rows))
	for i, r := range n.rows {
		cells[i] = make([]mbox, len(r))
		for j, c := range r {
			b := m.lay(c, cs)
			cells[i][j] = b
			colW[j] = math.Max(colW[j], b.w)
			rowAsc[i] = math.Max(rowAsc[i], b.asc)
			rowDesc[i] = math.Max(rowDesc[i], b.desc)
		}
		rowAsc[i] = math.Max(rowAsc[i], 0.7*s)
		rowDesc[i] = math.Max(rowDesc[i], 0.2*s)
	}
	colSep := 0.9 * s
	if n.align == "rl" {
		colSep = 0.1 * s
	}
	if n.align == "l" {
		colSep = 1.0 * s
	}
	rowSep := 0.35 * s
	colX := make([]float64, cols)
	x := 0.0
	for j := range colW {
		colX[j] = x
		x += colW[j] + colSep
	}
	w := math.Max(0, x-colSep)
	rowY := make([]float64, len(n.rows)) // baseline offset from the top of the grid
	y := 0.0
	for i := range n.rows {
		y += rowAsc[i]
		rowY[i] = y
		y += rowDesc[i] + rowSep
	}
	height := math.Max(0, y-rowSep)
	axis := 0.25 * s
	asc := height/2 + axis
	grid := mbox{w: w, asc: asc, desc: height - asc}
	grid.draw = func(dst *image.RGBA, ox, base float64) {
		top := base - asc
		for i, r := range cells {
			for j, c := range r {
				cx := ox + colX[j]
				switch {
				case n.align == "l" || (n.align == "rl" && j%2 == 1):
				case n.align == "rl":
					cx += colW[j] - c.w
				default:
					cx += (colW[j] - c.w) / 2
				}
				c.draw(dst, cx, top+rowY[i])
			}
		}
	}
	if n.left == "" && n.right == "" {
		return grid
	}
	if n.align == "l" { // cases: only a left brace
		return m.wrapDelims(grid, n.left, ".", st)
	}
	return m.wrapDelims(grid, n.left, n.right, st)
}

// strokeSeg draws an anti-aliased line segment.
func strokeSeg(dst *image.RGBA, x0, y0, x1, y1, th float64, col color.RGBA) {
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	nx, ny := -dy/l*th/2, dx/l*th/2
	px := [4]float64{x0 + nx, x1 + nx, x1 - nx, x0 - nx}
	py := [4]float64{y0 + ny, y1 + ny, y1 - ny, y0 - ny}
	minX, minY, maxX, maxY := px[0], py[0], px[0], py[0]
	for i := 1; i < 4; i++ {
		minX, maxX = math.Min(minX, px[i]), math.Max(maxX, px[i])
		minY, maxY = math.Min(minY, py[i]), math.Max(maxY, py[i])
	}
	r := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))).Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	// Rasterize the full quad inside the clipped rectangle's coordinate space.
	full := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
	z := vector.NewRasterizer(full.Dx(), full.Dy())
	z.MoveTo(float32(px[0]-float64(full.Min.X)), float32(py[0]-float64(full.Min.Y)))
	for i := 1; i < 4; i++ {
		z.LineTo(float32(px[i]-float64(full.Min.X)), float32(py[i]-float64(full.Min.Y)))
	}
	z.ClosePath()
	z.Draw(dst, full, image.NewUniform(col), image.Point{})
}
