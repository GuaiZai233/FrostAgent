package llm

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
)

func TestEnginePreservesNativeToolContinuation(t *testing.T) {
	for _, tc := range []struct {
		format               api.Format
		first, final, marker string
	}{
		{api.Responses, `{"output":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"},{"type":"function_call","call_id":"fixture-call","name":"lookup","arguments":"{}"}]}`, `{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`, "opaque-reasoning"},
		{api.Anthropic, `{"content":[{"type":"thinking","thinking":"hidden","signature":"opaque-signature"},{"type":"tool_use","id":"fixture-call","name":"lookup","input":{}}]}`, `{"content":[{"type":"text","text":"done"}]}`, "opaque-signature"},
		{api.Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}},"thoughtSignature":"opaque-signature"}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"done"}]}}]}`, "opaque-signature"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					fmt.Fprint(w, tc.first)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), tc.marker) || !strings.Contains(string(raw), "synthetic-tool-result") {
					t.Errorf("engine dropped continuation: %s", raw)
				}
				fmt.Fprint(w, tc.final)
			}))
			defer server.Close()
			engine := &Engine{MaxIterations: 3, ModelName: "test-model", Provider: api.NewClient(server.URL, "", tc.format, 0), ToolRegistry: map[string]ToolExecutor{"lookup": &staticTool{name: "lookup", result: "synthetic-tool-result"}}}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
			if result.Error != nil || result.Content != "done" || requests != 2 {
				t.Fatalf("engine result: %+v, requests=%d", result, requests)
			}
		})
	}
}
