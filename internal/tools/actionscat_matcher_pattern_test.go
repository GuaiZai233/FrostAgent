package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

func TestMatcherToolPreservesPatternWhitespace(t *testing.T) {
	scope := newTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "fixture-admin"})
	for _, matchType := range []string{"exact", "contains", "regex"} {
		t.Run(matchType, func(t *testing.T) {
			captured := make(chan actionscat.CreateMatcherReq, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req actionscat.CreateMatcherReq
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				captured <- req
				_ = json.NewEncoder(w).Encode(actionscat.Matcher{ID: "fixture-matcher", Pattern: req.Pattern})
			}))
			defer backend.Close()
			client := actionscat.New(func(key string) string {
				if key == "ACTIONSCAT_ENDPOINT" {
					return backend.URL
				}
				return ""
			})
			const pattern = " cat \n"
			args, _ := json.Marshal(map[string]string{"action_id": "fixture-action", "name": "fixture matcher", "match_type": matchType, "pattern": pattern})
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "fixture-admin"})
			out, err := ActionsCatCreateMatcherTool(client, scope).ExecuteContext(ctx, string(args))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-captured:
				if req.Pattern != pattern {
					t.Fatalf("pattern changed: got %q want %q", req.Pattern, pattern)
				}
			default:
				t.Fatalf("matcher not created: %s", out)
			}
		})
	}
}
