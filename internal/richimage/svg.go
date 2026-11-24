package richimage

import (
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

type svgText struct {
	x, y, size float64 // user units
	text       string
	fill       color.RGBA
	anchor     string
	bold       bool
}

type svgProps struct {
	fill   color.RGBA
	size   float64
	anchor string
	bold   bool
	tx, ty float64
}

// firstField returns the first whitespace/comma separated token, or "".
func firstField(s string) string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "px"))
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// applyAttr updates inherited text properties from one attribute.
func (p *svgProps) applyAttr(name, value string) {
	switch name {
	case "fill":
		if value == "none" || value == "currentColor" {
			return
		}
		if col, err := oksvg.ParseSVGColor(value); err == nil {
			r, g, b, a := col.RGBA()
			p.fill = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
		}
	case "font-size":
		if v, ok := parseFloat(value); ok && v > 0 {
			p.size = v
		}
	case "text-anchor":
		p.anchor = strings.TrimSpace(value)
	case "font-weight":
		v := strings.TrimSpace(value)
		n, _ := strconv.Atoi(v)
		p.bold = v == "bold" || v == "bolder" || n >= 600
	case "transform":
		if i := strings.Index(value, "translate("); i >= 0 {
			rest := value[i+len("translate("):]
			if j := strings.IndexByte(rest, ')'); j >= 0 {
				parts := strings.FieldsFunc(rest[:j], func(r rune) bool { return r == ',' || r == ' ' })
				if len(parts) > 0 {
					if x, ok := parseFloat(parts[0]); ok {
						p.tx += x
					}
				}
				if len(parts) > 1 {
					if y, ok := parseFloat(parts[1]); ok {
						p.ty += y
					}
				}
			}
		}
	}
}

// parseSVGTexts collects <text> content because oksvg only draws shapes.
func (c *canvas) parseSVGTexts(content string) []svgText {
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = false
	stack := []svgProps{{fill: c.th.fg, size: 16, anchor: "start"}}
	var out []svgText
	var inText int
	var curX, curY float64
	for {
		tok, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				return out
			}
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p := stack[len(stack)-1]
			var x, y, dx, dy *float64
			for _, a := range t.Attr {
				p.applyAttr(a.Name.Local, a.Value)
				switch a.Name.Local {
				case "style":
					for _, decl := range strings.Split(a.Value, ";") {
						if k, v, ok := strings.Cut(decl, ":"); ok {
							p.applyAttr(strings.TrimSpace(k), strings.TrimSpace(v))
						}
					}
				case "x":
					if v, ok := parseFloat(firstField(a.Value)); ok {
						x = &v
					}
				case "y":
					if v, ok := parseFloat(firstField(a.Value)); ok {
						y = &v
					}
				case "dx":
					if v, ok := parseFloat(a.Value); ok {
						dx = &v
					}
				case "dy":
					if v, ok := parseFloat(a.Value); ok {
						dy = &v
					}
				}
			}
			stack = append(stack, p)
			switch t.Name.Local {
			case "text":
				inText++
				curX, curY = 0, 0
				if x != nil {
					curX = *x
				}
				if y != nil {
					curY = *y
				}
			case "tspan":
				if x != nil {
					curX = *x
				}
				if y != nil {
					curY = *y
				}
			}
			if inText > 0 {
				if dx != nil {
					curX += *dx
				}
				if dy != nil {
					curY += *dy
				}
			}
		case xml.EndElement:
			if t.Name.Local == "text" && inText > 0 {
				inText--
			}
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if inText == 0 {
				continue
			}
			p := stack[len(stack)-1]
			s := strings.Join(strings.Fields(string(t)), " ")
			if s == "" {
				continue
			}
			out = append(out, svgText{x: curX + p.tx, y: curY + p.ty, size: p.size, text: s, fill: p.fill, anchor: p.anchor, bold: p.bold})
			// Advance the cursor so following tspans without x continue the line.
			curX += c.measure(s, style{bold: p.bold}, p.size)
		}
	}
}

// maxSVGElements bounds the work the third-party parser can be asked to do.
const maxSVGElements = 3000

// validateSVG rejects constructs the pinned oksvg parser expands without any
// cycle, depth or size limit (<use> and any href reference), which could
// overflow the stack or exhaust memory and cannot be recovered from.
func validateSVG(content string) error {
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = false
	elements := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil // malformed XML is reported by the parser itself
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if elements++; elements > maxSVGElements {
			return fmt.Errorf("SVG 元素过多（最多 %d 个）", maxSVGElements)
		}
		switch strings.ToLower(el.Name.Local) {
		case "use", "image", "foreignobject", "script", "symbol", "pattern", "mask", "filter":
			return fmt.Errorf("SVG 不支持 <%s> 元素", el.Name.Local)
		}
		for _, a := range el.Attr {
			if strings.EqualFold(a.Name.Local, "href") {
				return fmt.Errorf("SVG 不支持 href 引用，请直接内联图形")
			}
		}
	}
}

func (c *canvas) renderSVG(content string, width int, maxHeight int) (*image.RGBA, error) {
	if err := validateSVG(content); err != nil {
		return nil, err
	}
	if err := c.fs.checkText(content); err != nil {
		return nil, err
	}
	var icon *oksvg.SvgIcon
	var rerr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("SVG 解析失败: %v", r)
			}
		}()
		icon, rerr = oksvg.ReadIconStream(strings.NewReader(content), oksvg.IgnoreErrorMode)
	}()
	if rerr != nil {
		return nil, fmt.Errorf("SVG 解析失败: %w", rerr)
	}
	vb := icon.ViewBox
	if vb.W <= 0 || vb.H <= 0 {
		return nil, fmt.Errorf("SVG 需要 viewBox 或 width/height")
	}
	scale := float64(width) / vb.W
	height := int(vb.H*scale + 0.5)
	if height < 1 {
		return nil, fmt.Errorf("SVG 尺寸无效")
	}
	if height > maxHeight {
		return nil, fmt.Errorf("SVG 过高，请降低 viewBox 的高宽比")
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(c.th.bg), image.Point{}, draw.Src)
	func() {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("SVG 绘制失败: %v", r)
			}
		}()
		icon.SetTarget(0, 0, float64(width), float64(height))
		scanner := rasterx.NewScannerGV(width, height, img, img.Bounds())
		icon.Draw(rasterx.NewDasher(width, height, scanner), 1)
	}()
	if rerr != nil {
		return nil, rerr
	}
	for _, t := range c.parseSVGTexts(content) {
		size := t.size * scale
		if size < 4 || size > 400 {
			continue
		}
		st := style{bold: t.bold}
		w := c.measure(t.text, st, size)
		x := (t.x - vb.X) * scale
		switch t.anchor {
		case "middle":
			x -= w / 2
		case "end":
			x -= w
		}
		c.drawText(img, x, (t.y-vb.Y)*scale, t.text, st, size, t.fill)
	}
	return img, c.err
}
