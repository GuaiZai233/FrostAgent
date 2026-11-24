// Package richimage renders Markdown (with LaTeX math) and SVG into PNG images
// in pure Go, so bots on text-only platforms can show formulas and diagrams.
package richimage

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"strings"
)

const (
	FormatMarkdown = "markdown"
	FormatSVG      = "svg"

	DefaultWidth = 640
	MinWidth     = 320
	MaxWidth     = 1200

	MaxMarkdownBytes = 20000
	MaxSVGBytes      = 100000

	// scale renders at 2x so text stays crisp after QQ recompression.
	scale = 2
	// maxPixelHeight bounds memory and keeps the image readable in chat.
	maxPixelHeight = 6000
	// maxMathPixels caps all formula bitmaps of one render (about 32 MiB RGBA).
	maxMathPixels = 8 << 20
	// maxConcurrentRenders bounds memory when several sessions render at once.
	maxConcurrentRenders = 2
)

var renderSlots = make(chan struct{}, maxConcurrentRenders)

// Request describes one render.
type Request struct {
	Format  string
	Content string
	// Theme is "light" (default) or "dark". SVG always renders on light.
	Theme string
	// Width is the layout width in CSS pixels; the PNG is twice as wide.
	Width int
	// FontPath overrides the automatically discovered CJK font.
	FontPath string
}

// Render returns PNG bytes for the request.
func Render(req Request) ([]byte, error) { return RenderContext(context.Background(), req) }

// RenderContext is Render with admission control that honors cancellation: a
// caller that has gone away never waits for, or consumes, a render slot.
func RenderContext(ctx context.Context, req Request) (data []byte, err error) {
	select {
	case renderSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-renderSlots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Model-written input must surface as an error, never as a panic.
	defer func() {
		if r := recover(); r != nil {
			data, err = nil, fmt.Errorf("渲染内部错误: %v", r)
		}
	}()
	if strings.TrimSpace(req.Content) == "" {
		return nil, fmt.Errorf("content 不能为空")
	}
	width := req.Width
	if width == 0 {
		width = DefaultWidth
	}
	if width < MinWidth || width > MaxWidth {
		return nil, fmt.Errorf("width 必须在 %d..%d 之间", MinWidth, MaxWidth)
	}
	fonts, err := loadFonts(req.FontPath)
	if err != nil {
		return nil, err
	}
	fs := newFaces(fonts)
	defer fs.close()
	c := &canvas{fs: fs, th: lightTheme, scale: scale, mathBudget: maxMathPixels}
	pxWidth := width * scale

	switch req.Format {
	case FormatMarkdown:
		if len(req.Content) > MaxMarkdownBytes {
			return nil, fmt.Errorf("markdown 内容超过 %d 字节，请拆分成多张图", MaxMarkdownBytes)
		}
		switch req.Theme {
		case "", "light":
		case "dark":
			c.th = darkTheme
		default:
			return nil, fmt.Errorf("theme 必须是 light 或 dark")
		}
		height, err := renderMarkdown(c, req.Content, float64(pxWidth))
		if err != nil {
			return nil, err
		}
		if height > maxPixelHeight {
			return nil, fmt.Errorf("内容渲染后过长，请拆分成多张图")
		}
		return encode(c.finish(pxWidth, height))
	case FormatSVG:
		if len(req.Content) > MaxSVGBytes {
			return nil, fmt.Errorf("svg 内容超过 %d 字节", MaxSVGBytes)
		}
		img, err := c.renderSVG(req.Content, pxWidth, maxPixelHeight)
		if err != nil {
			return nil, err
		}
		return encode(img)
	default:
		return nil, fmt.Errorf("format 必须是 %s 或 %s", FormatMarkdown, FormatSVG)
	}
}

func encode(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("PNG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}
