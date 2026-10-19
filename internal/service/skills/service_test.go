package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	skillstore "FrostAgent/internal/skills"
	"FrostAgent/internal/storage"
)

func TestManagementAuthImportToggleAndDelete(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')"); err != nil {
		t.Fatal(err)
	}
	service := New(skillstore.NewStore(db, "synthetic-instance"), func(key string) string {
		if key == "ADMIN_TOKEN" {
			return "synthetic-admin-token"
		}
		return ""
	})
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	member, err := writer.Create("import/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = member.Write([]byte("---\nname: import\ndescription: Testing\n---\nInstructions"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body []byte, auth bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.RemoteAddr = "192.0.2.1:1234"
		if auth {
			request.Header.Set("Authorization", "Bearer synthetic-admin-token")
		}
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		return response
	}
	if response := call(http.MethodPost, "/api/skills/import", archive.Bytes(), false); response.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized import accepted")
	}
	if response := call(http.MethodPost, "/api/skills/import", archive.Bytes(), true); response.Code != 200 || !strings.Contains(response.Body.String(), "\"enabled\":false") {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/skills/items/import", []byte("{\"enabled\":true}"), true); response.Code != 200 {
		t.Fatal("toggle failed")
	}
	if response := call(http.MethodGet, "/api/skills/items/import", nil, true); response.Code != 200 || !strings.Contains(response.Body.String(), "\"enabled\":true") || strings.Contains(response.Body.String(), "package_json") {
		t.Fatal("details incorrect")
	}
	if response := call(http.MethodDelete, "/api/skills/items/import", nil, true); response.Code != 200 {
		t.Fatal("delete failed")
	}
	if response := call(http.MethodGet, "/api/skills/items/import", nil, true); response.Code != 404 {
		t.Fatal("deleted skill visible")
	}
}
