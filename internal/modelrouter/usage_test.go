package modelrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
)

func TestRoutedWorkloadsRecordUsageOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire map[string]any
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if wire["UsageModule"] != nil || wire["usage_module"] != nil {
			t.Error("local module metadata leaked upstream")
		}
		if r.URL.Path == "/embeddings" {
			fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":6,"total_tokens":6}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"synthetic"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`)
	}))
	defer server.Close()
	m := New(filepath.Join(t.TempDir(), "model_router.json"))
	cfg := lifecycleConfiguration()
	cfg.Endpoints[0].BaseURL = server.URL
	for _, workload := range Workloads {
		cfg.GlobalBindings[workload] = Binding{Mode: BindingModel, ModelID: "model_test"}
	}
	if err := m.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(); err != nil {
		t.Fatal(err)
	}
	usage, _ := tokenusage.New(nil, "", "")
	m.SetUsageStore(usage)
	for _, workload := range Workloads {
		if workload == WorkloadEmbedding {
			continue
		}
		provider := m.Provider(workload, false, time.Second)
		if _, err := provider.Chat(context.Background(), core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "synthetic"}}}); err != nil {
			t.Fatal(err)
		}
	}
	// The sticker parser rejects this text, but its model request still consumed tokens.
	v := sticker.LLMVisionCaller{Provider: m.Provider(WorkloadVision, false, time.Second), ModelName: "synthetic"}
	if _, _, _, err := v.Describe("c3ludGhldGlj", "image/png"); err == nil {
		t.Fatal("invalid sticker response unexpectedly accepted")
	}
	if _, err := m.Provider(WorkloadDialogue, false, time.Second).Chat(context.Background(), core.ChatRequest{UsageModule: "security"}); err != nil {
		t.Fatal(err)
	}
	_, embedding, err := m.EmbeddingSession(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedding.Embed(context.Background(), core.EmbeddingRequest{Input: []string{"synthetic"}}); err != nil {
		t.Fatal(err)
	}
	snap, err := usage.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range snap.Modules {
		want := int64(15)
		if row.Module == "embedding" {
			want = 6
		}
		if row.Requests != 1 || row.Total.Value != want {
			t.Errorf("incorrect attribution: %+v", row)
		}
	}
}
