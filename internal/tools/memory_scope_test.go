package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestMemoryToolGroupReadsOnlyAuthorizedScopes(t *testing.T) {
	for _, embedding := range []bool{false, true} {
		name := "keyword"
		if embedding {
			name = "embedding"
		}
		t.Run(name, func(t *testing.T) { exerciseMemoryToolGroupScopes(t, embedding) })
	}
}

func exerciseMemoryToolGroupScopes(t *testing.T, embedding bool) {
	root := t.TempDir()
	db, err := storage.OpenWithConfig(context.Background(), root, storage.Config{Backend: storage.SQLite, DSN: filepath.Join(root, "memory.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Test','now')`); err != nil {
		t.Fatal(err)
	}
	store := memory.NewSQLStore(db, "synthetic-instance")
	for _, owner := range []string{"synthetic-a", "synthetic-b", "telegram:user:synthetic-a"} {
		if err := store.Save(memory.MemoryEntry{ID: owner, Owner: owner, Content: "needle-" + owner, Tags: []string{"needle"}}); err != nil {
			t.Fatal(err)
		}
	}
	groups := memory.NewSQLGroupManager(db, "synthetic-instance", nil)
	group, err := groups.GetGroupStoreForPlatform("qq", "synthetic-group")
	if err != nil {
		t.Fatal(err)
	}
	// IDs may overlap across scopes; recall accounting must still target the right store.
	if err := group.Save(memory.MemoryEntry{ID: "synthetic-a", Owner: "synthetic-b", Content: "needle-group", Source: memory.SourceManual, Tags: []string{"needle"}}); err != nil {
		t.Fatal(err)
	}
	other, err := groups.GetGroupStoreForPlatform("qq", "other-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Save(memory.MemoryEntry{ID: "other-memory", Content: "needle-other", Source: memory.SourceManual}); err != nil {
		t.Fatal(err)
	}
	engine := &llm.Engine{MemoryReader: memory.NewReader(store, 20), MemoryWriter: memory.NewWriter(store), MemoryGateway: memory.NewGateway(), GroupManager: groups}
	if embedding {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			data := []map[string]any{}
			for i := range request.Input {
				data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}))
		defer server.Close()
		router := modelrouter.New(filepath.Join(root, "router.json"))
		config := router.Draft()
		config.Endpoints = []modelrouter.Endpoint{{ID: "synthetic-endpoint", DisplayName: "Test", BaseURL: server.URL + "/v1", Enabled: true}}
		config.Models = []modelrouter.Model{{ID: "synthetic-model", DisplayName: "Test", EndpointID: "synthetic-endpoint", UpstreamModel: "embed", Enabled: true, Capabilities: []string{"embedding"}}}
		config.GlobalBindings[modelrouter.WorkloadEmbedding] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "synthetic-model"}
		if err := router.SaveDraft(config); err != nil {
			t.Fatal(err)
		}
		if _, err := router.Publish(); err != nil {
			t.Fatal(err)
		}
		runtime := runtimescope.New(nil, nil, nil)
		defer func() { runtime.Cancel(); runtime.Wait() }()
		embeddings, err := memory.NewEmbeddings(runtime, store, groups, router, nil)
		if err != nil {
			t.Fatal(err)
		}
		engine.MemoryEmbeddings = embeddings
		for _, scope := range []memory.SearchScope{
			{Type: memory.ScopeGroup, Platform: "qq", GroupID: "synthetic-group"},
			{Type: memory.ScopeGroup, Platform: "qq", GroupID: "other-group"},
			{Type: memory.ScopePrivate, Owner: "synthetic-a"},
			{Type: memory.ScopePrivate, Owner: "synthetic-b"},
			{Type: memory.ScopePrivate, Owner: "telegram:user:synthetic-a"},
		} {
			embeddings.Update(scope)
		}
		embeddings.Wait()
	}
	tool := NewMemoryTool(engine)
	var wg sync.WaitGroup
	for _, actor := range []string{"synthetic-a", "synthetic-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "group:synthetic-group", OwnerType: memory.OwnerGroup, ActorUserID: actor, ActorPlatform: "onebot", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}})
			for _, args := range []string{`{"action":"search","tags":["needle"]}`, `{"action":"search","query":"needle","scope":"user"}`, `{"action":"list"}`} {
				result, err := tool.ExecuteContext(ctx, args)
				if err != nil {
					t.Error(err)
					return
				}
				var entries []memory.MemoryEntry
				if err := json.Unmarshal([]byte(result), &entries); err != nil {
					t.Error(result)
					return
				}
				want := 2
				if strings.Contains(args, `"scope":"user"`) {
					want = 1
				}
				if len(entries) != want {
					t.Errorf("%s: got %s", args, result)
				}
				for _, entry := range entries {
					if entry.ScopeType == memory.ScopePrivate && entry.Owner != actor || entry.ScopeType == memory.ScopeGroup && entry.GroupID != "synthetic-group" {
						t.Errorf("scope leak for %s: %s", actor, result)
					}
				}
			}
			result, err := tool.ExecuteContext(ctx, `{"action":"search","query":"needle","scope":"user","user_id":"synthetic-victim"}`)
			if err != nil || !strings.Contains(result, "身份由本轮发送者确定") {
				t.Errorf("arbitrary user accepted: %s %v", result, err)
			}
		}()
	}
	wg.Wait()
	for _, actor := range []string{"synthetic-a", "synthetic-b"} {
		ctx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "group:synthetic-group", ActorUserID: actor, ActorPlatform: "qq", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}})
		if _, err := tool.ExecuteContext(ctx, `{"action":"write","content":"group-written","tags":["written"],"scope":"user"}`); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.ListAll()
	if err != nil || len(entries) != 3 {
		t.Fatalf("group writes entered user scope: %#v %v", entries, err)
	}
	groupEntries, err := group.ListAll()
	if err != nil || len(groupEntries) != 3 {
		t.Fatalf("missing group writes: %#v %v", groupEntries, err)
	}
	for _, entry := range groupEntries {
		if entry.Owner != memory.GroupOwnerExplicit {
			t.Fatalf("member ownership survived: %#v", entry)
		}
	}
	var version int
	for _, entry := range groupEntries {
		if entry.ID == "synthetic-a" && entry.AccessCount != 2 {
			t.Fatalf("wrong group recall counter: %#v", entry)
		}
	}
	for _, entry := range entries {
		want := 2
		if entry.Owner == "telegram:user:synthetic-a" {
			want = 0
		}
		if entry.AccessCount != want {
			t.Fatalf("wrong user recall counter: %#v", entry)
		}
	}
	for _, run := range []llm.RunContext{
		{Owner: "group:synthetic-group", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}},
		{Owner: "group:synthetic-group", ActorUserID: "synthetic-a", ActorPlatform: "telegram", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}},
		{Owner: "group:synthetic-group", ActorUserID: "synthetic-a", ActorPlatform: "qq", Awareness: true, RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}},
	} {
		ctx := llm.WithRunContext(context.Background(), run)
		result, err := tool.ExecuteContext(ctx, `{"action":"list","scope":"user"}`)
		if err != nil || !strings.Contains(result, "没有所请求作用域的读取权限") {
			t.Fatalf("unauthenticated user scope accepted: %s %v", result, err)
		}
	}
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "synthetic-a"})
	result, err := tool.ExecuteContext(ctx, `{"action":"list"}`)
	if err != nil || strings.Contains(result, "needle-group") || strings.Contains(result, "needle-synthetic-b") {
		t.Fatalf("private scope leaked: %s %v", result, err)
	}
	result, err = tool.ExecuteContext(ctx, `{"action":"list","scope":"group"}`)
	if err != nil || !strings.Contains(result, "没有所请求作用域的读取权限") {
		t.Fatalf("private chat selected group: %s %v", result, err)
	}
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil || version != storage.SchemaVersion {
		t.Fatalf("schema changed: %d %v", version, err)
	}
}

func TestLegacyVisibilityDoesNotGrantAccess(t *testing.T) {
	store := memory.NewStore(filepath.Join(t.TempDir(), "brain.json"))
	var data memory.ExportData
	if err := json.Unmarshal([]byte(`{"version":1,"entries":[{"id":"legacy","owner":"synthetic-other","content":"needle","visibility":"public"}]}`), &data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ImportData(data, false); err != nil {
		t.Fatal(err)
	}
	engine := &llm.Engine{MemoryReader: memory.NewReader(store, 20), MemoryGateway: memory.NewGateway()}
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "synthetic-current"})
	result, err := NewMemoryTool(engine).ExecuteContext(ctx, `{"action":"search","query":"needle"}`)
	if err != nil || result != "未找到相关记忆" {
		t.Fatalf("legacy public leaked: %s %v", result, err)
	}
	entries, _ := store.ListAll()
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "visibility") || entries[0].Owner != "synthetic-other" {
		t.Fatalf("legacy import changed scope or retained visibility: %s", raw)
	}
}
