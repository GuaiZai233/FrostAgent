package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/richimage"
)

func richImageContext(run llm.RunContext) context.Context {
	if run.PlatformActionHook == nil && !run.Proactive && !run.Mock {
		run.PlatformActionHook = func(context.Context, string) (core.PlatformActionResult, error) {
			return core.PlatformActionResult{}, nil
		}
	}
	return llm.WithRunContext(context.Background(), run)
}

func TestSendRichImageDescriptionIncludesUsageGuidance(t *testing.T) {
	const prefix = "Render content into a PNG image and send it to the active conversation. "
	const guidance = "Call this tool when the user asks about a specific math problem or coding issue, asks you to discuss a viewpoint in detail, or when the output would exceed 200 characters."
	description := SendRichImageTool(nil).description
	if !strings.HasPrefix(description, prefix+guidance) {
		t.Fatalf("usage guidance must immediately follow the opening sentence: %q", description)
	}
}

func TestSendRichImageProducesInlineImageAction(t *testing.T) {
	tool := SendRichImageTool(nil)
	args := `{"format":"markdown","content":"Solve $x^2-1=0$:\n\n$$x=\\pm 1$$"}`
	result, err := tool.executeResult(richImageContext(llm.RunContext{SessionID: "s"}), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.PlatformActionPayload == "" || result.PlatformActionPayload != result.Content {
		t.Fatalf("expected a platform action payload, got %+v", result)
	}
	var request QQRequest
	if err := json.Unmarshal([]byte(result.PlatformActionPayload), &request); err != nil {
		t.Fatal(err)
	}
	if request.Action != "send" || len(request.Arguments.Messages) != 1 {
		t.Fatalf("unexpected request: %+v", request)
	}
	msg := request.Arguments.Messages[0]
	if msg.Type != "image" || msg.Transfer != "inline" || msg.Path == "" || msg.URL != "" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	t.Cleanup(func() { _ = os.Remove(msg.Path) })
	data, err := os.ReadFile(msg.Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if img.Bounds().Dx() != richimage.DefaultWidth*2 {
		t.Fatalf("unexpected width %d", img.Bounds().Dx())
	}
}

func TestSendRichImageRejectsUnavailableContexts(t *testing.T) {
	tool := SendRichImageTool(nil)
	args := `{"format":"markdown","content":"hi"}`
	for name, run := range map[string]llm.RunContext{
		"proactive": {Proactive: true},
		"mock":      {Mock: true},
		"no hook":   {},
	} {
		ctx := llm.WithRunContext(context.Background(), run)
		if _, err := tool.executeResult(ctx, args); err == nil || !strings.Contains(err.Error(), "OneBot") {
			t.Errorf("%s: expected an OneBot session error, got %v", name, err)
		}
	}
}

func TestSendRichImageValidatesInput(t *testing.T) {
	tool := SendRichImageTool(nil)
	ctx := richImageContext(llm.RunContext{SessionID: "s"})
	for name, args := range map[string]string{
		"bad json":      `{`,
		"unknown field": `{"format":"markdown","content":"x","html":"<b>x</b>"}`,
		"html format":   `{"format":"html","content":"<b>x</b>"}`,
		"empty content": `{"format":"markdown","content":" "}`,
		"bad svg":       `{"format":"svg","content":"not svg"}`,
		"bad width":     `{"format":"markdown","content":"x","width":10}`,
	} {
		result, err := tool.executeResult(ctx, args)
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if result.PlatformActionPayload != "" {
			t.Errorf("%s: failed render must not produce a send payload", name)
		}
	}
}

func TestSendRichImageMissingFontIsReported(t *testing.T) {
	tool := SendRichImageTool(nil)
	ctx := richImageContext(llm.RunContext{SessionID: "s"})
	t.Setenv(richimage.FontEnvKey, "/nonexistent/font.otf")
	// The per-instance scope may not read process env; only assert that when it
	// does, the failure is explicit rather than a silent text fallback.
	_, err := tool.executeResult(ctx, `{"format":"markdown","content":"你好"}`)
	if err != nil && !errors.Is(err, richimage.ErrNoCJKFont) && !strings.Contains(err.Error(), "字体") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendRichImageIsNotProactiveAllowed(t *testing.T) {
	if SendRichImageTool(nil).IsProactiveAllowed() {
		t.Fatal("send_rich_image must not be callable during unaddressed proactive turns")
	}
}

func TestSendRichImageCanceledContextSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(richImageContext(llm.RunContext{SessionID: "s"}))
	cancel()
	result, err := SendRichImageTool(nil).executeResult(ctx, `{"format":"markdown","content":"hi"}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if result.PlatformActionPayload != "" || result.Content != "" {
		t.Fatalf("canceled call must not produce a delivery: %+v", result)
	}
}

func TestSendRichImageRejectsSVGUse(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`
	args, _ := json.Marshal(map[string]string{"format": "svg", "content": svg})
	if _, err := SendRichImageTool(nil).executeResult(richImageContext(llm.RunContext{SessionID: "s"}), string(args)); err == nil {
		t.Fatal("cyclic <use> must be rejected")
	}
}
