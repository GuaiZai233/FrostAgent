package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	securityctl "github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestAuditHTTPPaginationValidationAndScope(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	controller := securityctl.NewControllerSQL(db)
	for n, id := range []string{"a", "b", "c"} {
		instance := "synthetic-target"
		if n == 1 {
			instance = "synthetic-other"
		}
		if err := controller.Audit.Append(securityctl.AuditEvent{ID: id, Instance: instance, At: time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewScoped(controller, func(key string) string {
		if key == "MCP_CONTROL_TOKEN" {
			return "synthetic-token"
		}
		return ""
	})
	for _, tc := range []struct {
		query  string
		status int
		id     string
	}{
		{"?limit=bad", 400, ""}, {"?limit=", 400, ""}, {"?limit=0", 400, ""}, {"?limit=-1", 400, ""}, {"?limit=1001", 400, ""},
		{"?offset=-1", 400, ""}, {"?offset=bad", 400, ""}, {"?offset=10001", 400, ""},
		{"?instance=synthetic-target&limit=1&offset=1", 200, "a"},
		{"?instance=synthetic-target&id=b", 404, ""}, {"?instance=synthetic-other&id=b", 200, "b"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/security/audit"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer synthetic-token")
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		if tc.id != "" {
			var result struct {
				Events []securityctl.AuditEvent `json:"events"`
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Events) != 1 || result.Events[0].ID != tc.id {
				t.Fatalf("%s: %s", tc.query, w.Body.String())
			}
		}
	}
}
