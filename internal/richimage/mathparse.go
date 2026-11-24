package richimage

import (
	"fmt"
	"strings"
)

// A small LaTeX math parser covering what homework-style answers need:
// scripts, fractions, roots, delimiters, text, accents and matrices.

type mnode any

type mseq []mnode

const (
	kOrd = iota
	kBin
	kRel
	kOpen
	kClose
	kPunct
	kOp
)

const (
	fontItalic = iota
	fontRoman
	fontBold
)

type mAtom struct {
	text   string
	kind   int
	font   int
	big    bool // large operator drawn bigger and centered on the axis
	limits bool // scripts go above/below in display style
}

type mScripts struct {
	base     mnode
	sup, sub mnode
}

type mFrac struct {
	num, den mnode
	bar      bool
	delims   bool // \binom
}

type mSqrt struct {
	body, index mnode
}

type mDelim struct {
	left, right string
	body        mnode
}

type mText struct{ text string }

type mAccent struct {
	kind string
	body mnode
}

type mSpace struct{ em float64 }

type mStyled struct {
	body mnode
	font int
}

type mMatrix struct {
	rows        [][]mseq
	left, right string
	align       string // "c", "l" or "rl" (alternating, for aligned)
}

type mparser struct {
	rs  []rune
	i   int
	err error
}

func parseMath(src string) (mseq, error) {
	p := &mparser{rs: []rune(src)}
	seq, term := p.parseSeq("")
	if p.err != nil {
		return nil, p.err
	}
	if term != "" {
		return nil, fmt.Errorf("公式结构不匹配")
	}
	return seq, nil
}

func (p *mparser) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
}

func (p *mparser) eof() bool { return p.i >= len(p.rs) }

func (p *mparser) skipSpace() {
	for !p.eof() && (p.rs[p.i] == ' ' || p.rs[p.i] == '\n' || p.rs[p.i] == '\t' || p.rs[p.i] == '\r') {
		p.i++
	}
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// readCmd reads the command name after a backslash.
func (p *mparser) readCmd() string {
	if p.eof() {
		return ""
	}
	if !isLetter(p.rs[p.i]) {
		p.i++
		return string(p.rs[p.i-1])
	}
	start := p.i
	for !p.eof() && isLetter(p.rs[p.i]) {
		p.i++
	}
	name := string(p.rs[start:p.i])
	if !p.eof() && p.rs[p.i] == '*' && (name == "align" || name == "gather") {
		p.i++
		name += "*"
	}
	return name
}

// parseSeq parses until a terminator. end is "}" , "]" or "" (top level/environment).
// It returns "}", "]", "&", "\\\\", "right", "end" or "" at EOF.
func (p *mparser) parseSeq(end string) (mseq, string) {
	var seq mseq
	for p.err == nil {
		p.skipSpace()
		if p.eof() {
			return seq, ""
		}
		c := p.rs[p.i]
		switch {
		case c == '}':
			p.i++
			if end == "}" {
				return seq, "}"
			}
			p.fail("多余的 }")
			return seq, ""
		case c == ']' && end == "]":
			p.i++
			return seq, "]"
		case c == '&':
			p.i++
			return seq, "&"
		case c == '{':
			p.i++
			inner, term := p.parseSeq("}")
			if term != "}" {
				p.fail("缺少 }")
				return seq, ""
			}
			seq = append(seq, inner)
		case c == '^' || c == '_':
			p.i++
			arg := p.parseArg()
			seq = attachScript(seq, c == '^', arg)
		case c == '\'':
			p.i++
			seq = attachScript(seq, true, &mAtom{text: "′", kind: kOrd, font: fontRoman})
		case c == '\\':
			p.i++
			cmd := p.readCmd()
			switch cmd {
			case "\\":
				return seq, "\\\\"
			case "right":
				return seq, "right"
			case "end":
				return seq, "end"
			}
			if n := p.command(cmd); n != nil {
				seq = append(seq, n)
			}
		default:
			p.i++
			seq = append(seq, atomFromRune(c))
		}
	}
	return seq, ""
}

func attachScript(seq mseq, sup bool, arg mnode) mseq {
	var base mnode
	if len(seq) > 0 {
		base = seq[len(seq)-1]
		seq = seq[:len(seq)-1]
	}
	sc, ok := base.(*mScripts)
	if !ok || (sup && sc.sup != nil) || (!sup && sc.sub != nil) {
		sc = &mScripts{base: base}
	}
	if sup {
		sc.sup = arg
	} else {
		sc.sub = arg
	}
	return append(seq, sc)
}

// parseArg reads a braced group or a single token.
func (p *mparser) parseArg() mnode {
	p.skipSpace()
	if p.eof() {
		p.fail("缺少参数")
		return mseq{}
	}
	c := p.rs[p.i]
	switch c {
	case '{':
		p.i++
		inner, term := p.parseSeq("}")
		if term != "}" {
			p.fail("缺少 }")
		}
		return inner
	case '\\':
		p.i++
		if n := p.command(p.readCmd()); n != nil {
			return n
		}
		return mseq{}
	}
	p.i++
	return atomFromRune(c)
}

// rawGroup reads the raw text inside {...}, honoring nested braces.
func (p *mparser) rawGroup() string {
	p.skipSpace()
	if p.eof() || p.rs[p.i] != '{' {
		p.fail("缺少 {")
		return ""
	}
	p.i++
	depth := 1
	start := p.i
	for !p.eof() {
		switch p.rs[p.i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				s := string(p.rs[start:p.i])
				p.i++
				return s
			}
		}
		p.i++
	}
	p.fail("缺少 }")
	return ""
}

func (p *mparser) delimiter() string {
	p.skipSpace()
	if p.eof() {
		p.fail("缺少分隔符")
		return ""
	}
	c := p.rs[p.i]
	p.i++
	if c != '\\' {
		return string(c)
	}
	switch name := p.readCmd(); name {
	case "{", "lbrace":
		return "{"
	case "}", "rbrace":
		return "}"
	case "|", "Vert":
		return "‖"
	case "langle":
		return "⟨"
	case "rangle":
		return "⟩"
	case "lfloor":
		return "⌊"
	case "rfloor":
		return "⌋"
	case "lceil":
		return "⌈"
	case "rceil":
		return "⌉"
	case "vert":
		return "|"
	default:
		p.fail("不支持的分隔符 \\%s", name)
		return ""
	}
}

func (p *mparser) command(cmd string) mnode {
	switch cmd {
	case "frac", "dfrac", "tfrac":
		num := p.parseArg()
		return &mFrac{num: num, den: p.parseArg(), bar: true}
	case "binom":
		num := p.parseArg()
		return &mFrac{num: num, den: p.parseArg(), delims: true}
	case "sqrt":
		var index mnode
		p.skipSpace()
		if !p.eof() && p.rs[p.i] == '[' {
			p.i++
			idx, term := p.parseSeq("]")
			if term != "]" {
				p.fail("缺少 ]")
			}
			index = idx
		}
		return &mSqrt{body: p.parseArg(), index: index}
	case "left":
		l := p.delimiter()
		body, term := p.parseSeq("")
		if term != "right" {
			p.fail("缺少 \\right")
			return nil
		}
		return &mDelim{left: l, right: p.delimiter(), body: body}
	case "begin":
		return p.environment(p.rawGroup())
	case "text", "textrm", "mathrm", "operatorname", "mbox", "mathsf", "textbf":
		return &mText{text: p.rawGroup()}
	case "mathbf", "boldsymbol", "bm":
		return &mStyled{body: p.parseArg(), font: fontBold}
	case "mathit", "mathcal", "mathscr", "mathfrak":
		return p.parseArg()
	case "mathbb":
		raw := p.rawGroup()
		var sb strings.Builder
		for _, r := range raw {
			if v, ok := blackboard[r]; ok {
				sb.WriteString(v)
			} else {
				sb.WriteRune(r)
			}
		}
		return &mText{text: sb.String()}
	case "hat", "widehat", "bar", "overline", "underline", "vec", "overrightarrow", "tilde", "widetilde", "dot", "ddot":
		return &mAccent{kind: cmd, body: p.parseArg()}
	case "displaystyle", "textstyle", "scriptstyle", "limits", "nolimits", "big", "Big", "bigg", "Bigg":
		return nil
	case ",", "thinspace":
		return &mSpace{0.17}
	case ":", "medspace":
		return &mSpace{0.22}
	case ";", "thickspace", " ":
		return &mSpace{0.28}
	case "!":
		return &mSpace{-0.17}
	case "quad":
		return &mSpace{1}
	case "qquad":
		return &mSpace{2}
	case "{", "}", "%", "$", "#", "&", "_":
		return &mAtom{text: cmd, kind: kOrd, font: fontRoman}
	case "|":
		return &mAtom{text: "‖", kind: kOrd, font: fontRoman}
	}
	if a, ok := mathSymbols[cmd]; ok {
		return &mAtom{text: a.text, kind: a.kind, font: a.font, big: a.big, limits: a.limits}
	}
	if limits, ok := mathFunctions[cmd]; ok {
		return &mAtom{text: cmd, kind: kOp, font: fontRoman, limits: limits}
	}
	p.fail("不支持的命令 \\%s", cmd)
	return nil
}

var blackboard = map[rune]string{'R': "ℝ", 'N': "ℕ", 'Z': "ℤ", 'Q': "ℚ", 'C': "ℂ", 'P': "ℙ"}

func (p *mparser) environment(name string) mnode {
	m := &mMatrix{align: "c"}
	switch name {
	case "matrix", "smallmatrix":
	case "pmatrix":
		m.left, m.right = "(", ")"
	case "bmatrix":
		m.left, m.right = "[", "]"
	case "Bmatrix":
		m.left, m.right = "{", "}"
	case "vmatrix":
		m.left, m.right = "|", "|"
	case "Vmatrix":
		m.left, m.right = "‖", "‖"
	case "cases":
		m.left, m.align = "{", "l"
	case "array":
		p.rawGroup() // column spec is ignored: columns are centered
	case "aligned", "align", "align*", "split", "gather", "gather*", "alignedat":
		m.align = "rl"
		if name == "alignedat" {
			p.rawGroup()
		}
	default:
		p.fail("不支持的环境 %s", name)
		return nil
	}
	var row []mseq
	for p.err == nil {
		cell, term := p.parseSeq("")
		row = append(row, cell)
		switch term {
		case "&":
		case "\\\\":
			m.rows = append(m.rows, row)
			row = nil
		case "end":
			if got := p.rawGroup(); got != name {
				p.fail("\\begin{%s} 与 \\end{%s} 不匹配", name, got)
			}
			if len(row) > 1 || len(row[0]) > 0 {
				m.rows = append(m.rows, row)
			}
			if len(m.rows) == 0 {
				p.fail("空的 %s 环境", name)
			}
			return m
		default:
			p.fail("缺少 \\end{%s}", name)
		}
	}
	return nil
}

func atomFromRune(c rune) mnode {
	switch {
	case isLetter(c):
		return &mAtom{text: string(c), kind: kOrd, font: fontItalic}
	case c >= '0' && c <= '9':
		return &mAtom{text: string(c), kind: kOrd, font: fontRoman}
	}
	switch c {
	case '+':
		return &mAtom{text: "+", kind: kBin, font: fontRoman}
	case '-':
		return &mAtom{text: "−", kind: kBin, font: fontRoman}
	case '*':
		return &mAtom{text: "∗", kind: kBin, font: fontRoman}
	case '=', '<', '>':
		return &mAtom{text: string(c), kind: kRel, font: fontRoman}
	case '(', '[':
		return &mAtom{text: string(c), kind: kOpen, font: fontRoman}
	case ')', ']':
		return &mAtom{text: string(c), kind: kClose, font: fontRoman}
	case ',', ';':
		return &mAtom{text: string(c), kind: kPunct, font: fontRoman}
	}
	return &mAtom{text: string(c), kind: kOrd, font: fontRoman}
}

type mathSym struct {
	text        string
	kind, font  int
	big, limits bool
}

var mathSymbols = buildMathSymbols()

// mathFunctions maps function-like operators to whether they take limits.
var mathFunctions = map[string]bool{
	"sin": false, "cos": false, "tan": false, "cot": false, "sec": false, "csc": false,
	"arcsin": false, "arccos": false, "arctan": false, "sinh": false, "cosh": false, "tanh": false,
	"log": false, "ln": false, "lg": false, "exp": false, "deg": false, "dim": false, "ker": false, "arg": false,
	"lim": true, "limsup": true, "liminf": true, "max": true, "min": true, "sup": true, "inf": true,
	"det": true, "gcd": true, "Pr": true,
}

func buildMathSymbols() map[string]mathSym {
	m := map[string]mathSym{}
	add := func(kind, font int, pairs ...string) {
		for i := 0; i+1 < len(pairs); i += 2 {
			m[pairs[i]] = mathSym{text: pairs[i+1], kind: kind, font: font}
		}
	}
	add(kOrd, fontItalic, "alpha", "α", "beta", "β", "gamma", "γ", "delta", "δ", "epsilon", "ε", "varepsilon", "ε",
		"zeta", "ζ", "eta", "η", "theta", "θ", "vartheta", "ϑ", "iota", "ι", "kappa", "κ", "lambda", "λ", "mu", "μ",
		"nu", "ν", "xi", "ξ", "pi", "π", "varpi", "ϖ", "rho", "ρ", "varrho", "ϱ", "sigma", "σ", "varsigma", "ς",
		"tau", "τ", "upsilon", "υ", "phi", "φ", "varphi", "φ", "chi", "χ", "psi", "ψ", "omega", "ω")
	add(kOrd, fontRoman, "Gamma", "Γ", "Delta", "Δ", "Theta", "Θ", "Lambda", "Λ", "Xi", "Ξ", "Pi", "Π",
		"Sigma", "Σ", "Upsilon", "Υ", "Phi", "Φ", "Psi", "Ψ", "Omega", "Ω",
		"infty", "∞", "partial", "∂", "nabla", "∇", "forall", "∀", "exists", "∃", "nexists", "∄",
		"emptyset", "∅", "varnothing", "∅", "neg", "¬", "lnot", "¬", "angle", "∠", "triangle", "△",
		"degree", "°", "ldots", "…", "dots", "…", "cdots", "⋯", "vdots", "⋮", "ddots", "⋱", "hbar", "ℏ", "ell", "ℓ",
		"Re", "ℜ", "Im", "ℑ", "aleph", "ℵ", "prime", "′", "therefore", "∴", "because", "∵", "mid", "∣",
		"uparrow", "↑", "downarrow", "↓", "lbrace", "{", "rbrace", "}", "langle", "⟨", "rangle", "⟩",
		"lfloor", "⌊", "rfloor", "⌋", "lceil", "⌈", "rceil", "⌉", "vert", "|", "Vert", "‖", "backslash", "\\",
		"blacksquare", "■", "square", "□", "Box", "□", "qed", "■")
	add(kBin, fontRoman, "pm", "±", "mp", "∓", "times", "×", "div", "÷", "cdot", "∙", "ast", "∗", "star", "⋆",
		"circ", "∘", "bullet", "•", "cap", "∩", "cup", "∪", "wedge", "∧", "land", "∧", "vee", "∨", "lor", "∨",
		"oplus", "⊕", "otimes", "⊗", "setminus", "∖")
	add(kRel, fontRoman, "leq", "≤", "le", "≤", "geq", "≥", "ge", "≥", "neq", "≠", "ne", "≠", "approx", "≈",
		"equiv", "≡", "sim", "∼", "simeq", "≃", "cong", "≅", "propto", "∝", "in", "∈", "notin", "∉", "ni", "∋",
		"subset", "⊂", "supset", "⊃", "subseteq", "⊆", "supseteq", "⊇", "ll", "≪", "gg", "≫", "perp", "⊥",
		"parallel", "∥", "to", "→", "rightarrow", "→", "leftarrow", "←", "leftrightarrow", "↔", "Rightarrow", "⇒",
		"Leftarrow", "⇐", "Leftrightarrow", "⇔", "implies", "⇒", "iff", "⇔", "mapsto", "↦", "gets", "←")
	for name, text := range map[string]string{"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬",
		"iiint": "∭", "oint": "∮", "bigcup": "⋃", "bigcap": "⋂", "bigoplus": "⨁", "bigwedge": "⋀", "bigvee": "⋁"} {
		m[name] = mathSym{text: text, kind: kOp, font: fontRoman, big: true,
			limits: name == "sum" || name == "prod" || name == "coprod" || strings.HasPrefix(name, "big")}
	}
	return m
}
