package tools

import (
	"context"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCreateScheduleUsesTrustedConversationDelivery(t *testing.T) {
	scope := newTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "fixture-user"})
	for _, tc := range []struct {
		name, platform, adapter, group string
		drop                           bool
	}{
		{"onebot private", "onebot", "onebot", "", false},
		{"astrbot group", "fixture-platform", "astrbot", "fixture-group", false},
		{"legacy nonchat", "", "", "", false},
		{"older server", "fixture-platform", "astrbot", "fixture-group", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan actionscat.CreateScheduleReq, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/actions/fixture-action/schedules" || r.Header.Get("Authorization") != "Bearer fixture-management-token" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var req actionscat.CreateScheduleReq
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				captured <- req
				delivery := req.DeliveryContext
				if tc.drop {
					delivery = nil
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(actionscat.Schedule{ID: "fixture-schedule", ActionID: "fixture-action", DeliveryContext: delivery})
			}))
			defer server.Close()
			client := actionscat.New(func(k string) string {
				switch k {
				case "ACTIONSCAT_ENDPOINT":
					return server.URL
				case "ACTIONSCAT_MANAGEMENT_TOKEN":
					return "fixture-management-token"
				}
				return ""
			})
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "fixture-user", ActorPlatform: tc.platform, AdapterID: tc.adapter, SessionID: "fixture-session", RouteScope: modelrouter.Scope{GroupID: tc.group}})
			out, err := ActionsCatCreateScheduleTool(client, scope).ExecuteContext(ctx, `{"action_id":"fixture-action","cron_expr":"* * * * *","delivery_context":{"platform":"attacker-platform","target_id":"attacker-target"}}`)
			if err != nil {
				t.Fatal(err)
			}
			var expected *actionscat.DeliveryContext
			if tc.platform != "" {
				expected = &actionscat.DeliveryContext{Platform: tc.platform, AdapterID: tc.adapter, MessageType: "private", TargetID: "fixture-user", UserID: "fixture-user", SessionID: "fixture-session", GroupID: tc.group}
				if tc.group != "" {
					expected.MessageType = "group"
					expected.TargetID = tc.group
				}
			}
			select {
			case req := <-captured:
				if !reflect.DeepEqual(req.DeliveryContext, expected) {
					t.Fatalf("untrusted destination: %+v; expected %+v", req.DeliveryContext, expected)
				}
			default:
				t.Fatalf("no schedule request: %s", out)
			}
			if tc.drop && !strings.Contains(out, "未确认保存") {
				t.Fatalf("old server capability not reported: %s", out)
			}
			if !tc.drop && strings.Contains(out, "未确认保存") {
				t.Fatalf("unexpected compatibility warning: %s", out)
			}
		})
	}
}
