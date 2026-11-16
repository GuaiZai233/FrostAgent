package instance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func TestDatabaseManagerRequiresDatabase(t *testing.T) {
	manager, err := NewDatabaseWithDB(t.TempDir(), nil)
	if err == nil || manager != nil {
		t.Fatalf("manager without a database = %v, err=%v", manager, err)
	}
}

func TestSQLSecurityBackupHTTPRoundTrip(t *testing.T) {
	m := testManager(t)
	principal, err := security.NewPrincipal("qq", "synthetic-backup-user")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.security.Access.Lock(principal, "synthetic backup reason"); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/instances/global/"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		m.ServeHTTP(response, req)
		return response
	}
	response := request(http.MethodGet, "backup/security", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("Content-Disposition") != `attachment; filename="global-security.json"` ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("security backup: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	saved := append([]byte(nil), response.Body.Bytes()...)
	var state backup.GlobalSecurity
	if err := json.Unmarshal(saved, &state); err != nil {
		t.Fatal(err)
	}
	if state.FormatVersion != backup.FormatVersion || len(state.AccessRecords) != 1 ||
		state.AccessRecords[0].Principal != principal || state.AccessRecords[0].Reason != "synthetic backup reason" {
		t.Fatalf("security backup lost access state: %+v", state)
	}
	for _, invalid := range [][]byte{[]byte("["), []byte(`{"format_version":999}`)} {
		response := request(http.MethodPost, "import/security", invalid)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid security import: status=%d body=%s", response.Code, response.Body.String())
		}
		if locked, _, err := m.security.Access.IsLocked(principal); err != nil || !locked {
			t.Fatalf("invalid import changed access state: locked=%t err=%v", locked, err)
		}
	}
	empty, err := json.Marshal(backup.GlobalSecurity{FormatVersion: backup.FormatVersion})
	if err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodPost, "import/security", empty)
	if response.Code != http.StatusOK {
		t.Fatalf("reset security: %d %s", response.Code, response.Body.String())
	}
	if locked, _, err := m.security.Access.IsLocked(principal); err != nil || locked {
		t.Fatalf("security reset did not clear access state: locked=%t err=%v", locked, err)
	}
	response = request(http.MethodPost, "import/security", saved)
	if response.Code != http.StatusOK {
		t.Fatalf("restore security: %d %s", response.Code, response.Body.String())
	}
	if locked, _, err := m.security.Access.IsLocked(principal); err != nil || !locked {
		t.Fatalf("security JSON round trip lost access state: locked=%t err=%v", locked, err)
	}
	if _, err := m.db.SQL.Exec("UPDATE access_records SET strike_times_json = 'invalid'"); err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodGet, "backup/security", nil)
	if response.Code != http.StatusBadRequest || response.Header().Get("Content-Disposition") != "" {
		t.Fatalf("corrupt security state produced a backup: status=%d headers=%v", response.Code, response.Header())
	}
}
