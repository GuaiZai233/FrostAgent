package backup

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/storage"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestZIPExportRejectsExpandedContentOverLimit(t *testing.T) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	budget := zipBudget{limit: 8}
	if err := addJSON(writer, &budget, "small.json", "12345678"); err == nil {
		t.Fatal("JSON larger than restore limit was exported")
	}
	if budget.expanded != 0 {
		t.Fatalf("rejected JSON consumed budget: %d", budget.expanded)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	imageDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(imageDir, "image.png"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	writer = zip.NewWriter(&output)
	budget = zipBudget{limit: 4}
	if err := addStickerFiles(writer, &budget, []sticker.Entry{{FileName: "image.png"}}, imageDir); err == nil {
		t.Fatal("image larger than restore limit was exported")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceZIPSeparatesImagesAndBlanksSecrets(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "backup.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const id = "test-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES (?, ?, ?)`, id, "Test", "now"); err != nil {
		t.Fatal(err)
	}
	settings, err := instanceconfig.OpenDatabase(db, id, false)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"BOT_NAME": "Test Bot", "UPSTREAM_API_KEY": "synthetic-private-key",
		"CUSTOM_TOKEN": "synthetic-custom-token",
	} {
		if err := settings.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	mcpStore := mcp.NewSQLConfigStore(db, id)
	if err := mcpStore.Save(&mcp.Config{Servers: []mcp.ServerConfig{{
		ID: "server-a", Name: "A", Transport: mcp.TransportConfig{
			Type: mcp.TransportStreamableHTTP, URL: "https://example.com/mcp?token=synthetic-query-key",
			Headers: map[string]string{"Authorization": "synthetic-header-key"},
			Env:     map[string]string{"KEY": "synthetic-env-key"},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(t.TempDir(), "images")
	stickers, err := sticker.NewSQLStore(db, id, imageDir)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2}
	if err := stickers.Add("sticker-a", "image.png", image); err != nil {
		t.Fatal(err)
	}

	archive, err := BuildInstanceZIP(db, id, imageDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"synthetic-private-key", "synthetic-custom-token", "synthetic-query-key", "synthetic-header-key", "synthetic-env-key"} {
		if bytes.Contains(archive, []byte(secret)) {
			t.Fatalf("ZIP contains secret %q", secret)
		}
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, member := range reader.File {
		r, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[member.Name] = data
	}
	if !bytes.Equal(files["sticker/files/image.png"], image) {
		t.Fatal("sticker image bytes were not preserved")
	}
	var exported Settings
	if err := json.Unmarshal(files["setting.json"], &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Values["BOT_NAME"] != "Test Bot" || exported.Values["UPSTREAM_API_KEY"] != "" || exported.Values["CUSTOM_TOKEN"] != "" {
		t.Fatalf("settings export did not preserve safe fields and blank secrets: %#v", exported.Values)
	}
	if len(exported.MCP.Servers) != 1 || exported.MCP.Servers[0].Transport.URL != "https://example.com/mcp" {
		t.Fatalf("MCP URL was not stripped: %#v", exported.MCP.Servers)
	}
	for _, name := range []string{"manifest.json", "memory.json", "group_summaries.json", "sticker/metadata.json"} {
		if len(files[name]) == 0 {
			t.Fatalf("missing ZIP member %s", name)
		}
	}
}
