package richimage

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const (
	phOpen  = ''
	phClose = ''
)

type mathItem struct {
	expr    string
	display bool
}

// extractMath replaces $...$ and $$...$$ with placeholders so Markdown never
// touches LaTeX punctuation. Code spans and fenced blocks are left alone.
func extractMath(src string) (string, []mathItem) {
	var items []mathItem
	var out strings.Builder
	lines := strings.SplitAfter(src, "\n")
	var seg strings.Builder
	flushSeg := func() {
		out.WriteString(extractMathSegment(seg.String(), &items))
		seg.Reset()
	}
	fence := ""
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence == "" {
			if len(line)-len(trimmed) <= 3 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
				flushSeg()
				fence = trimmed[:3]
				out.WriteString(line)
				continue
			}
			seg.WriteString(line)
			continue
		}
		out.WriteString(line)
		if strings.HasPrefix(trimmed, fence) {
			fence = ""
		}
	}
	flushSeg()
	return out.String(), items
}

func placeholder(i int) string { return string(phOpen) + strconv.Itoa(i) + string(phClose) }

func extractMathSegment(s string, items *[]mathItem) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			out.WriteString(s[i : i+2])
			i += 2
		case s[i] == '`':
			n := 0
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			ticks := s[i : i+n]
			end := strings.Index(s[i+n:], ticks)
			if end < 0 {
				out.WriteString(ticks)
				i += n
				continue
			}
			out.WriteString(s[i : i+n+end+n])
			i += n + end + n
		case strings.HasPrefix(s[i:], "$$"):
			end := strings.Index(s[i+2:], "$$")
			expr := ""
			if end >= 0 {
				expr = strings.TrimSpace(s[i+2 : i+2+end])
			}
			if end < 0 || expr == "" {
				out.WriteString("$$")
				i += 2
				continue
			}
			lineStart := strings.LastIndexByte(s[:i], '\n') + 1
			after := i + 2 + end + 2
			lineEnd := strings.IndexByte(s[after:], '\n')
			if lineEnd < 0 {
				lineEnd = len(s) - after
			}
			*items = append(*items, mathItem{expr: expr, display: true})
			ph := placeholder(len(*items) - 1)
			if strings.TrimSpace(s[lineStart:i]) == "" && strings.TrimSpace(s[after:after+lineEnd]) == "" {
				ph = "\n\n" + ph + "\n\n"
			}
			out.WriteString(ph)
			i = after
		case s[i] == '$':
			end := -1
			for j := i + 1; j < len(s) && s[j] != '\n'; j++ {
				if s[j] == '\\' {
					j++
					continue
				}
				if s[j] == '$' {
					end = j
					break
				}
			}
			valid := end > i+1
			if valid {
				expr := s[i+1 : end]
				valid = validInlineMath(expr)
				if valid && end+1 < len(s) && s[end+1] >= '0' && s[end+1] <= '9' {
					valid = false
				}
			}
			if !valid {
				out.WriteByte('$')
				i++
				continue
			}
			*items = append(*items, mathItem{expr: s[i+1 : end]})
			out.WriteString(placeholder(len(*items) - 1))
			i = end + 1
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

type mdRenderer struct {
	c        *canvas
	src      []byte
	math     []mathItem
	base     float64
	left     float64
	right    float64
	y        float64
	listNest int
}

func (m *mdRenderer) width() float64 { return m.right - m.left }

func renderMarkdown(c *canvas, content string, width float64) (float64, error) {
	prepared, items := extractMath(scrubReserved(content))
	src := []byte(prepared)
	md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))
	doc := md.Parser().Parse(text.NewReader(src))
	pad := 20 * c.scale
	m := &mdRenderer{c: c, src: src, math: items, base: 16 * c.scale, left: pad, right: width - pad, y: pad}
	m.blocks(doc)
	if c.err != nil {
		return 0, c.err
	}
	return m.y + pad, nil
}

func (m *mdRenderer) gap(f float64) { m.y += m.base * f }

func (m *mdRenderer) blocks(parent ast.Node) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		m.block(n)
	}
}

func (m *mdRenderer) block(n ast.Node) {
	c := m.c
	switch n := n.(type) {
	case *ast.Heading:
		scales := [...]float64{0, 1.8, 1.5, 1.25, 1.1, 1.0, 0.95}
		size := m.base * scales[n.Level]
		m.gap(0.5)
		atoms := m.inline(n, style{bold: true}, size)
		h := c.flow(atoms, m.left, m.y, m.width(), 0)
		m.y += h
		if n.Level <= 2 {
			m.gap(0.25)
			c.hline(m.left, m.y, m.width(), maxFloat(1, c.scale), c.th.border)
			m.y += maxFloat(1, c.scale)
		}
		m.gap(0.5)
	case *ast.Paragraph, *ast.TextBlock:
		if display, ok := m.soleDisplayMath(n); ok {
			m.displayMath(display)
			return
		}
		atoms := m.inline(n, style{}, m.base)
		m.y += c.flow(atoms, m.left, m.y, m.width(), 0)
		m.gap(0.6)
	case *ast.List:
		m.list(n)
	case *ast.Blockquote:
		bar := c.deferRect()
		top := m.y
		oldLeft := m.left
		m.left += 18 * c.scale
		m.blocks(n)
		m.left = oldLeft
		*bar = rect{x: oldLeft, y: top, w: 4 * c.scale, h: m.y - top - m.base*0.4, col: c.th.border, set: true}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		m.codeBlock(n)
	case *ast.ThematicBreak:
		m.gap(0.3)
		c.hline(m.left, m.y, m.width(), maxFloat(1, c.scale), c.th.border)
		m.y += maxFloat(1, c.scale)
		m.gap(0.7)
	case *ast.HTMLBlock:
		var sb strings.Builder
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			sb.Write(seg.Value(m.src))
		}
		m.rawText(sb.String())
	case *east.Table:
		m.table(n)
	default:
		if n.HasChildren() {
			m.blocks(n)
		}
	}
}

func (m *mdRenderer) rawText(s string) {
	st := style{code: true}
	size := m.base * 0.9
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		atoms := m.c.textAtoms(line, st, size)
		m.y += m.c.flow(atoms, m.left, m.y, m.width(), 0)
	}
	m.gap(0.6)
}

func (m *mdRenderer) list(l *ast.List) {
	c := m.c
	idx := l.Start
	if idx == 0 {
		idx = 1
	}
	m.listNest++
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "•"
		if l.IsOrdered() {
			marker = strconv.Itoa(idx) + "."
			idx++
		} else if m.listNest > 1 {
			marker = "◦"
		}
		oldLeft := m.left
		indent := 26 * c.scale
		mw := c.measure(marker, style{}, m.base)
		if mw+8*c.scale > indent {
			indent = mw + 8*c.scale
		}
		startY := m.y
		m.left += indent
		savedOps := len(c.ops)
		m.blocks(item)
		// Draw the marker on the first line's baseline: reuse flow metrics.
		mx, my := oldLeft+indent-mw-8*c.scale, startY
		base := m.base
		insert := func(d *image.RGBA) {
			baseline := my + base*0.55/2 + base*0.86
			c.drawText(d, mx, baseline, marker, style{}, base, c.th.muted)
		}
		c.ops = append(c.ops[:savedOps:savedOps], append([]func(*image.RGBA){insert}, c.ops[savedOps:]...)...)
		m.left = oldLeft
	}
	m.listNest--
	if m.listNest == 0 {
		m.gap(0.2)
	}
}

func (m *mdRenderer) codeBlock(n ast.Node) {
	c := m.c
	size := m.base * 0.85
	pad := 12 * c.scale
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		lines = append(lines, strings.TrimRight(string(seg.Value(m.src)), "\r\n"))
	}
	bg := c.deferRect()
	top := m.y
	m.y += pad
	st := style{code: true}
	if c.err == nil {
		c.err = c.fs.checkText(strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		line = strings.ReplaceAll(line, "\t", "    ")
		var chunk []rune
		cw := 0.0
		emit := func() {
			s := string(chunk)
			y := m.y
			x := m.left + pad
			ls := size * 1.5
			c.ops = append(c.ops, func(d *image.RGBA) {
				c.drawText(d, x, y+ls/2+size*0.33, s, st, size, c.th.fg)
			})
			m.y += ls
			chunk, cw = nil, 0
		}
		for _, r := range line {
			w := c.measure(string(r), st, size)
			if cw+w > m.width()-2*pad && len(chunk) > 0 {
				emit()
			}
			chunk = append(chunk, r)
			cw += w
		}
		emit()
	}
	m.y += pad
	*bg = rect{x: m.left, y: top, w: m.width(), h: m.y - top, col: c.th.codeBG, set: true}
	m.gap(0.6)
}

func (m *mdRenderer) table(t *east.Table) {
	c := m.c
	type cell struct {
		atoms []atom
		align east.Alignment
	}
	var rows [][]cell
	var headerRows int
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		var row []cell
		_, isHeader := r.(*east.TableHeader)
		for cn := r.FirstChild(); cn != nil; cn = cn.NextSibling() {
			al := east.AlignNone
			if tc, ok := cn.(*east.TableCell); ok {
				al = tc.Alignment
			}
			row = append(row, cell{atoms: m.inline(cn, style{bold: isHeader}, m.base*0.95), align: al})
		}
		rows = append(rows, row)
		if isHeader {
			headerRows++
		}
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return
	}
	cp := 8 * c.scale
	natural := make([]float64, cols)
	for _, r := range rows {
		for i, cl := range r {
			w := 0.0
			for _, a := range cl.atoms {
				w += a.w
			}
			natural[i] = maxFloat(natural[i], w+2*cp)
		}
	}
	avail := m.width()
	total := 0.0
	for _, w := range natural {
		total += w
	}
	widths := append([]float64(nil), natural...)
	if total > avail {
		minW := math.Min(3*m.base+2*cp, avail/float64(cols))
		fixedW, flex := 0.0, 0.0
		for i, w := range natural {
			widths[i] = minW
			if w <= minW {
				fixedW += w
			} else {
				flex += w - minW
				fixedW += minW
			}
		}
		if flex > 0 && avail > fixedW {
			for i, w := range natural {
				if w > minW {
					widths[i] = minW + (w-minW)*(avail-fixedW)/flex
				}
			}
		}
		for i, w := range natural {
			if w < minW {
				widths[i] = w
			}
		}
		// Rounding can leave a hair over; the columns must fit inside the canvas.
		sum := 0.0
		for _, w := range widths {
			sum += w
		}
		if sum > avail {
			for i := range widths {
				widths[i] *= avail / sum
			}
		}
		for i, w := range widths {
			if w < natural[i]-0.5 && w-2*cp < m.base*0.8 {
				c.fail(fmt.Errorf("表格列太多，无法在当前宽度内显示；请增大 width 或拆分表格"))
				return
			}
		}
	}
	tw := 0.0
	for _, w := range widths {
		tw += w
	}
	top := m.y
	for ri, r := range rows {
		rowBG := c.deferRect()
		rowTop := m.y
		h := 0.0
		x := m.left
		for i := 0; i < cols; i++ {
			if i < len(r) {
				align := 0
				if r[i].align == east.AlignCenter {
					align = 1
				}
				h = maxFloat(h, c.flow(r[i].atoms, x+cp, rowTop+cp*0.6, widths[i]-2*cp, align)+cp*1.2)
			}
			x += widths[i]
		}
		h = maxFloat(h, m.base*1.4)
		if ri < headerRows {
			*rowBG = rect{x: m.left, y: rowTop, w: tw, h: h, col: c.th.codeBG, set: true}
		}
		m.y += h
		c.hline(m.left, m.y, tw, maxFloat(1, c.scale), c.th.border)
	}
	bottom := m.y
	c.hline(m.left, top, tw, maxFloat(1, c.scale), c.th.border)
	x := m.left
	for i := 0; i <= cols; i++ {
		xx := x
		c.ops = append(c.ops, func(d *image.RGBA) { fillRect(d, xx, top, maxFloat(1, c.scale), bottom-top, c.th.border) })
		if i < cols {
			x += widths[i]
		}
	}
	m.gap(0.7)
}

// soleDisplayMath detects a paragraph that only holds one display formula.
func (m *mdRenderer) soleDisplayMath(n ast.Node) (mathItem, bool) {
	if n.ChildCount() != 1 {
		return mathItem{}, false
	}
	t, ok := n.FirstChild().(*ast.Text)
	if !ok {
		return mathItem{}, false
	}
	s := strings.TrimSpace(string(t.Segment.Value(m.src)))
	idx, ok := parsePlaceholder(s)
	if !ok || idx < 0 || idx >= len(m.math) || !m.math[idx].display {
		return mathItem{}, false
	}
	return m.math[idx], true
}

func parsePlaceholder(s string) (int, bool) {
	r := []rune(s)
	if len(r) < 3 || r[0] != phOpen || r[len(r)-1] != phClose {
		return 0, false
	}
	idx, err := strconv.Atoi(string(r[1 : len(r)-1]))
	return idx, err == nil
}

func (m *mdRenderer) displayMath(it mathItem) {
	atoms := m.mathAtoms(it, m.base)
	m.gap(0.2)
	m.y += m.c.flow(atoms, m.left, m.y, m.width(), 1)
	m.gap(0.6)
}

func (m *mdRenderer) mathAtoms(it mathItem, size float64) []atom {
	mathSize := size
	if it.display {
		mathSize = size * 1.15
	}
	img, base, err := m.c.mathImage(it.expr, mathSize, it.display, m.c.th.fg)
	if err != nil {
		// Show the source instead of failing the whole image.
		lit := "$" + it.expr + "$"
		if it.display {
			lit = "$$" + it.expr + "$$"
		}
		return m.c.textAtoms(lit, style{code: true}, size*0.9)
	}
	return []atom{{img: img, ibase: base, size: size, w: float64(img.Bounds().Dx())}}
}

func decode(b []byte) string {
	return string(util.ResolveNumericReferences(util.ResolveEntityNames(util.UnescapePunctuations(b))))
}

// inline flattens inline children into atoms.
func (m *mdRenderer) inline(parent ast.Node, st style, size float64) []atom {
	var out []atom
	var walk func(n ast.Node, st style)
	walk = func(n ast.Node, st style) {
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch v := ch.(type) {
			case *ast.Text:
				out = append(out, m.textWithMath(string(v.Segment.Value(m.src)), true, st, size)...)
				if v.HardLineBreak() {
					out = append(out, atom{br: true, size: size})
				} else if v.SoftLineBreak() {
					out = append(out, atom{text: " ", st: st, size: size, w: m.c.measure(" ", st, size), space: true})
				}
			case *ast.String:
				out = append(out, m.textWithMath(string(v.Value), false, st, size)...)
			case *ast.CodeSpan:
				var sb strings.Builder
				for t := v.FirstChild(); t != nil; t = t.NextSibling() {
					if tx, ok := t.(*ast.Text); ok {
						sb.Write(tx.Segment.Value(m.src))
					}
				}
				cs := st
				cs.code = true
				out = append(out, m.c.textAtoms(strings.ReplaceAll(sb.String(), "\n", " "), cs, size*0.9)...)
			case *ast.Emphasis:
				es := st
				if v.Level >= 2 {
					es.bold = true
				}
				walk(v, es)
			case *east.Strikethrough:
				ss := st
				ss.strike = true
				walk(v, ss)
			case *ast.Link:
				ls := st
				ls.link = true
				walk(v, ls)
			case *ast.AutoLink:
				ls := st
				ls.link = true
				out = append(out, m.c.textAtoms(string(v.URL(m.src)), ls, size)...)
			case *ast.Image:
				var alt strings.Builder
				for t := v.FirstChild(); t != nil; t = t.NextSibling() {
					if tx, ok := t.(*ast.Text); ok {
						alt.Write(tx.Segment.Value(m.src))
					}
				}
				out = append(out, m.c.textAtoms(fmt.Sprintf("[图片: %s]", alt.String()), style{}, size)...)
			case *ast.RawHTML:
				for i := 0; i < v.Segments.Len(); i++ {
					seg := v.Segments.At(i)
					out = append(out, m.c.textAtoms(string(seg.Value(m.src)), st, size)...)
				}
			default:
				walk(ch, st)
			}
		}
	}
	walk(parent, st)
	return out
}

// scrubReserved replaces the private-use runes reserved for math placeholders,
// so user text can never impersonate a generated placeholder.
func scrubReserved(s string) string {
	if !strings.ContainsAny(s, string([]rune{phOpen, phClose})) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == phOpen || r == phClose {
			return '?'
		}
		return r
	}, s)
}

// textWithMath splits generated placeholders back into math atoms. Text between
// placeholders is entity-decoded (when raw) and scrubbed of reserved runes.
func (m *mdRenderer) textWithMath(s string, raw bool, st style, size float64) []atom {
	piece := func(p string) string {
		if raw {
			p = decode([]byte(p))
		}
		return scrubReserved(p)
	}
	var out []atom
	runes := []rune(s)
	start := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] != phOpen {
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != phClose {
			j++
		}
		if j >= len(runes) {
			break
		}
		idx, err := strconv.Atoi(string(runes[i+1 : j]))
		if err != nil || idx < 0 || idx >= len(m.math) {
			continue
		}
		out = append(out, m.c.textAtoms(piece(string(runes[start:i])), st, size)...)
		out = append(out, m.mathAtoms(m.math[idx], size)...)
		start = j + 1
		i = j
	}
	return append(out, m.c.textAtoms(piece(string(runes[start:])), st, size)...)
}

// validInlineMath applies the usual rule (no space just inside the dollars)
// but accepts an escaped space ("\ ") at the trailing edge, as in $\ x\ $.
func validInlineMath(expr string) bool {
	if strings.TrimSpace(expr) == "" || strings.TrimLeft(expr, " ") != expr {
		return false
	}
	trail := strings.TrimRight(expr, " ")
	return trail == expr || strings.HasSuffix(trail, "\\")
}
