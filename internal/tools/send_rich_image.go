package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/richimage"
)

const (
	// SendRichImageToolName is reserved so MCP servers cannot shadow it.
	SendRichImageToolName = "send_rich_image"

	richImageDirName = "frostagent-rich-image"
	// Rendered files are only needed until the adapter has read them.
	richImageMaxAge = 30 * time.Minute
)

// SendRichImageTool renders Markdown (with LaTeX math) or SVG to a PNG and
// sends it to the active conversation, for platforms that only carry text.
// The theme mode is read per call through getenv, so setting changes apply to
// the next render without a restart.
func SendRichImageTool(getenv func(string) string) Tool {
	return Tool{
		name: SendRichImageToolName,
		description: "Render content into a PNG image and send it to the active conversation. Call this tool when the user asks about a specific math problem or coding issue, asks you to discuss a viewpoint in detail, or when the output would exceed 200 characters. Use it when plain text cannot express the answer well: math derivations and formulas, tables, structured notes, code listings, or diagrams of abstract concepts. 推荐一旦遇到代码或数学公式等，优先调用此工具，避免直接输出被后端分隔逻辑错误解析造成歧义。 " +
			"Math supports a subset of LaTeX, using $...$ for inline math and $$...$$ for display math. Common formulas, matrices, cases, and aligned equations are supported. Unsupported commands are displayed as literal source text. " +
			"format=svg: a complete <svg> with a viewBox, for diagrams, flowcharts, graphs and layouts. Use inline presentation attributes (fill, stroke, font-size, text-anchor) on shapes and <text>; CSS <style>, classes, scripts, external images and <foreignObject> are not supported. HTML is not supported. " +
			"The image is sent as one message; keep text in the normal reply short and do not repeat the image content. Content that renders too tall must be split across several calls.",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"format": map[string]any{
					"type":        "string",
					"enum":        []string{richimage.FormatMarkdown, richimage.FormatSVG},
					"description": "markdown (with LaTeX math) or svg.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": fmt.Sprintf("The Markdown (up to %d bytes) or SVG (up to %d bytes) source.", richimage.MaxMarkdownBytes, richimage.MaxSVGBytes),
				},
				"theme": map[string]any{
					"type":        "string",
					"enum":        []string{"light", "dark"},
					"default":     "light",
					"description": "Color theme for markdown. SVG always renders on a white background.",
				},
				"width": map[string]any{
					"type":        "integer",
					"minimum":     richimage.MinWidth,
					"maximum":     richimage.MaxWidth,
					"default":     richimage.DefaultWidth,
					"description": "Layout width in CSS pixels; the PNG is twice as wide. Use a wider value for wide tables or long formulas.",
				},
			},
			"required":             []string{"format", "content"},
			"additionalProperties": false,
		},
		executeResult: func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
			run, _ := llm.RunContextFromContext(ctx)
			if run.Proactive || run.Mock || run.PlatformActionHook == nil {
				return textResult(""), fmt.Errorf("send_rich_image 需要当前活动的 OneBot 会话")
			}
			var input struct {
				Format  string `json:"format"`
				Content string `json:"content"`
				Theme   string `json:"theme"`
				Width   int    `json:"width"`
			}
			decoder := json.NewDecoder(strings.NewReader(args))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return textResult(""), fmt.Errorf("send_rich_image 参数不合法: %w", err)
			}
			// An invalid stored value cannot pass validation on save, so it can only
			// come from a hand-edited environment; fall back to the model default.
			var raw string
			if getenv != nil {
				raw = getenv(richimage.ThemeModeKey)
			}
			mode, err := richimage.ParseThemeMode(raw)
			if err != nil {
				mode = richimage.ThemeModeModel
			}
			data, err := richimage.RenderContext(ctx, richimage.Request{
				Format:  input.Format,
				Content: input.Content,
				Theme:   richimage.EffectiveTheme(mode, input.Theme, time.Now()),
				Width:   input.Width,
				// Fonts are a deployment-level setting, read from the process environment.
				FontPath: strings.TrimSpace(os.Getenv(richimage.FontEnvKey)),
				FontDir:  strings.TrimSpace(os.Getenv(richimage.FontDirEnvKey)),
			})
			if err != nil {
				return textResult(""), fmt.Errorf("渲染失败: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return textResult(""), err
			}
			path, err := writeRichImage(data)
			if err != nil {
				return textResult(""), err
			}
			messages := []Msg{{Type: "image", Path: path, Transfer: "inline"}}
			if err := ValidateQQMessages(messages, false); err != nil {
				return textResult(""), err
			}
			request, _ := json.Marshal(QQRequest{Action: "send", Arguments: QQArguments{Messages: messages}})
			return core.ToolExecutionResult{Content: string(request), PlatformActionPayload: string(request)}, nil
		},
	}
}

// writeRichImage stores the PNG in a private temp directory and removes stale files.
func writeRichImage(data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), richImageDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("无法创建临时目录: %w", err)
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > richImageMaxAge {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	file, err := os.CreateTemp(dir, "rich-*.png")
	if err != nil {
		return "", fmt.Errorf("无法写入临时图片: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("无法写入临时图片: %w", err)
	}
	return file.Name(), nil
}
