package richimage

import (
	"image"
	"math"
)

// Commands beyond the core set: sized delimiters, \not, \checkmark, \cancel,
// braces with labels, stacked and extensible arrows, and more symbols.

type mSized struct {
	text  string
	scale float64
}

type mCheck struct{}

type mCancel struct{ body mnode }

type mBrace struct {
	body  mnode
	under bool
}

type mStack struct {
	top, under mnode // either may be nil
	base       mnode
}

type mXArrow struct {
	label mnode
	left  bool
}

var sizedScale = map[string]float64{
	"big": 1.2, "bigl": 1.2, "bigr": 1.2,
	"Big": 1.8, "Bigl": 1.8, "Bigr": 1.8,
	"bigg": 2.4, "biggl": 2.4, "biggr": 2.4,
	"Bigg": 3.0, "Biggl": 3.0, "Biggr": 3.0,
}

// extraCommand parses commands handled outside the core switch. ok is false
// when cmd is not one of them.
func (p *mparser) extraCommand(cmd string) (mnode, bool) {
	if sc, ok := sizedScale[cmd]; ok {
		return &mSized{text: p.delimiter(), scale: sc}, true
	}
	switch cmd {
	case "checkmark":
		return &mCheck{}, true
	case "cancel":
		return &mCancel{body: p.parseArg()}, true
	case "underbrace":
		return &mBrace{body: p.parseArg(), under: true}, true
	case "overbrace":
		return &mBrace{body: p.parseArg()}, true
	case "stackrel", "overset":
		top := p.parseArg()
		return &mStack{top: top, base: p.parseArg()}, true
	case "underset":
		under := p.parseArg()
		return &mStack{under: under, base: p.parseArg()}, true
	case "xrightarrow", "xleftarrow":
		return &mXArrow{label: p.parseArg(), left: cmd == "xleftarrow"}, true
	case "not":
		return p.negated(), true
	}
	return nil, false
}

// negated handles \not: \not= and \not\in have precomposed forms; any other
// relation gets a combining long solidus overlay.
func (p *mparser) negated() mnode {
	p.skipSpace()
	if p.eof() {
		p.fail("\\not 后缺少符号")
		return nil
	}
	if p.rs[p.i] == '\\' {
		p.i++
		name := p.readCmd()
		if name == "in" {
			return &mAtom{text: "∉", kind: kRel, font: fontRoman}
		}
		if sym, ok := mathSymbols[name]; ok {
			return &mAtom{text: sym.text + "̸", kind: kRel, font: fontRoman}
		}
		p.fail("\\not 不支持 \\%s", name)
		return nil
	}
	c := p.rs[p.i]
	p.i++
	if c == '=' {
		return &mAtom{text: "≠", kind: kRel, font: fontRoman}
	}
	return &mAtom{text: string(c) + "̸", kind: kRel, font: fontRoman}
}

// addExtraSymbols registers the symbols of extraCommand's neighbours.
func addExtraSymbols(m map[string]mathSym) {
	put := func(kind int, pairs ...string) {
		for i := 0; i+1 < len(pairs); i += 2 {
			m[pairs[i]] = mathSym{text: pairs[i+1], kind: kind, font: fontRoman}
		}
	}
	put(kRel,
		"leqslant", "≤", "geqslant", "≥", "lt", "<", "gt", ">", "doteq", "≐",
		"nmid", "∤", "subsetneq", "⊊", "supsetneq", "⊋",
		"impliedby", "⟸", "longrightarrow", "⟶", "longleftarrow", "⟵",
		"Longrightarrow", "⟹", "Longleftarrow", "⟸", "longleftrightarrow", "⟷",
		"Longleftrightarrow", "⟺")
	put(kBin, "odot", "⊙")
	put(kOrd,
		"surd", "√", "lvert", "|", "rvert", "|", "lVert", "‖", "rVert", "‖",
		"top", "⊤", "bot", "⊥")
	put(kRel, "vdash", "⊢", "models", "⊨")
	m["oiint"] = mathSym{text: "∯", kind: kOp, font: fontRoman, big: true}
	m["bigotimes"] = mathSym{text: "⨂", kind: kOp, font: fontRoman, big: true, limits: true}
}

// centerOnAxis places a delimiter so its middle sits on the math axis.
func centerOnAxis(inner mbox, s float64) mbox {
	shift := 0.25*s - (inner.asc-inner.desc)/2
	return mbox{
		w:    inner.w,
		asc:  inner.asc + shift,
		desc: inner.desc - shift,
		draw: func(dst *image.RGBA, x, base float64) { inner.draw(dst, x, base-shift) },
	}
}

// layExtra lays out the nodes defined in this file.
func (m *mathLayout) layExtra(n mnode, st mstyle) (mbox, bool) {
	s := st.size
	col := m.col
	th := math.Max(1.2, 0.05*s)
	switch n := n.(type) {
	case *mSized:
		return centerOnAxis(m.strBox(n.text, fontRoman, s*n.scale), s), true

	case *mCheck:
		w := 0.75 * s
		box := mbox{w: w, asc: 0.8 * s, desc: 0.02 * s}
		box.draw = func(dst *image.RGBA, x, b float64) {
			strokeSeg(dst, x+0.02*w, b-0.42*s, x+0.36*w, b-0.02*s, math.Max(th, 0.08*s), col)
			strokeSeg(dst, x+0.36*w, b-0.02*s, x+0.98*w, b-0.8*s, math.Max(th, 0.08*s), col)
		}
		return box, true

	case *mCancel:
		body := m.lay(n.body, st)
		box := body
		inner := body.draw
		box.draw = func(dst *image.RGBA, x, b float64) {
			inner(dst, x, b)
			strokeSeg(dst, x, b+body.desc, x+body.w, b-body.asc, th, col)
		}
		return box, true

	case *mBrace:
		body := m.lay(n.body, st)
		gap, tick := 0.12*s, 0.12*s
		box := mbox{w: body.w, asc: body.asc, desc: body.desc}
		if n.under {
			box.desc = body.desc + gap + tick + 0.22*s
		} else {
			box.asc = body.asc + gap + tick + 0.22*s
		}
		box.draw = func(dst *image.RGBA, x, b float64) {
			body.draw(dst, x, b)
			if n.under {
				y := b + body.desc + gap
				strokeSeg(dst, x, y, x+body.w, y, th, col)
				strokeSeg(dst, x, y, x, y-tick, th, col)
				strokeSeg(dst, x+body.w, y, x+body.w, y-tick, th, col)
				return
			}
			y := b - body.asc - gap
			strokeSeg(dst, x, y, x+body.w, y, th, col)
			strokeSeg(dst, x, y, x, y+tick, th, col)
			strokeSeg(dst, x+body.w, y, x+body.w, y+tick, th, col)
		}
		return box, true

	case *mStack:
		ss := st.script()
		base := m.lay(n.base, st)
		var top, under mbox
		hasTop, hasUnder := n.top != nil, n.under != nil
		if hasTop {
			top = m.lay(n.top, ss)
		}
		if hasUnder {
			under = m.lay(n.under, ss)
		}
		gap := 0.1 * s
		w := math.Max(base.w, math.Max(top.w, under.w))
		asc, desc := base.asc, base.desc
		topUp, underDown := 0.0, 0.0
		if hasTop {
			topUp = base.asc + gap + top.desc
			asc = topUp + top.asc
		}
		if hasUnder {
			underDown = base.desc + gap + under.asc
			desc = underDown + under.desc
		}
		box := mbox{w: w, asc: asc, desc: desc}
		box.draw = func(dst *image.RGBA, x, b float64) {
			base.draw(dst, x+(w-base.w)/2, b)
			if hasTop {
				top.draw(dst, x+(w-top.w)/2, b-topUp)
			}
			if hasUnder {
				under.draw(dst, x+(w-under.w)/2, b+underDown)
			}
		}
		return box, true

	case *mXArrow:
		label := m.lay(n.label, st.script())
		w := math.Max(2.4*s, label.w+0.6*s) + 0.4*s
		axisUp := 0.25 * s
		labelUp := axisUp + 0.2*s + label.desc
		box := mbox{w: w, asc: labelUp + label.asc, desc: 0.1 * s}
		box.draw = func(dst *image.RGBA, x, b float64) {
			label.draw(dst, x+(w-label.w)/2, b-labelUp)
			y := b - axisUp
			x0, x1 := x+0.2*s, x+w-0.2*s
			strokeSeg(dst, x0, y, x1, y, th, col)
			tip, back := x1, x1-0.25*s
			if n.left {
				tip, back = x0, x0+0.25*s
			}
			strokeSeg(dst, tip, y, back, y-0.12*s, th, col)
			strokeSeg(dst, tip, y, back, y+0.12*s, th, col)
		}
		return box, true
	}
	return mbox{}, false
}

// centeredLabels reports whether scripts on base sit centred above and below
// it (limits, braces) rather than beside it.
func centeredLabels(base mnode, st mstyle) bool {
	switch b := base.(type) {
	case *mAtom:
		return b.limits && st.display
	case *mBrace:
		return true
	}
	return false
}
