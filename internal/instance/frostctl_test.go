package instance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestImportSettingsRuntimeFailureDoesNotPanic(t *testing.T) {
	m := testManager(t)
	info, err := m.Create("Synthetic import failure")
	if err != nil {
		t.Fatal(err)
	}
	builder := m.buildRuntime
	m.buildRuntime = func(*Manager, *managed, *instanceconfig.Store, bool) (*Runtime, error) {
		return nil, errors.New("synthetic runtime failure")
	}
	defer func() { m.buildRuntime = builder }()
	err = m.ImportSettings(info.ID, backup.Settings{
		FormatVersion: backup.FormatVersion,
		Values:        map[string]string{"BOT_NAME": "synthetic"},
	})
	if err == nil {
		t.Fatal("runtime rebuild failure was not reported")
	}
	if m.instances[info.ID].runtime != nil {
		t.Fatal("failed import retained a runtime")
	}
}

func TestBackupPreflightReadOnlyAndDisabledRestore(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Synthetic source")
	if err != nil {
		t.Fatal(err)
	}
	zip, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, next := m.List()
	filesBefore, _ := os.ReadDir(filepath.Join(root, "instances"))
	catalogBefore, err := modelrouter.NewSQL(m.db, "").ActiveChecked()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.VerifyBackup(context.Background(), zip, "Synthetic restored")
	if err != nil || plan["success"] != true {
		t.Fatal(plan, err)
	}
	after, afterNext := m.List()
	filesAfter, _ := os.ReadDir(filepath.Join(root, "instances"))
	if !reflect.DeepEqual(before, after) || next != afterNext || len(filesBefore) != len(filesAfter) {
		t.Fatal("preflight created an instance or directory")
	}
	catalogAfter, _ := modelrouter.NewSQL(m.db, "").ActiveChecked()
	if !reflect.DeepEqual(catalogBefore, catalogAfter) {
		t.Fatal("preflight changed shared catalog")
	}
	if _, err := m.VerifyBackup(context.Background(), zip[:len(zip)/2], "Synthetic"); err == nil {
		t.Fatal("truncated backup accepted")
	}
	conflict, err := m.VerifyBackup(context.Background(), zip, "Synthetic source")
	if err != nil || conflict["success"] != false {
		t.Fatal("name conflict omitted", err)
	}
	decoded, err := backup.DecodeInstanceZIP(zip)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Manifest.DatabaseSchemaVersion == 0 || decoded.Manifest.ApplicationVersion == "" {
		t.Fatal("version metadata missing")
	}
	decoded.Manifest.DatabaseMinimumCompatibleVersion = 9999
	decoded.Manifest.DatabaseSchemaVersion = 9999
	if _, err := backup.PreviewRestore(context.Background(), m.db, decoded); err == nil {
		t.Fatal("future schema accepted")
	}
	restored, err := m.RestoreInstanceZIP("Synthetic restored", zip)
	if err != nil || restored.Enabled || restored.ID == source.ID {
		t.Fatal(restored, err)
	}
	if err := m.PrepareDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	infos, _ := m.List()
	for _, info := range infos {
		if info.ID == restored.ID && (info.Enabled || info.Deleting) {
			t.Fatal("cancel enabled instance")
		}
	}
	if err := m.PrepareDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsInvalidArchiveRecords(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Synthetic backup")
	if err != nil {
		t.Fatal(err)
	}
	original, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"group_summaries.json":  `{"format_version":1,"records":[{"session_id":"group:synthetic","summary":""}]}`,
		"memory.json":           `{"format_version":1,"private_entries":[{"id":""}]}`,
		"sticker/metadata.json": `{"format_version":1,"entries":[{"id":"","file_name":"missing.png"}]}`,
	}
	for member, replacement := range cases {
		t.Run(member, func(t *testing.T) {
			reader, _ := zip.NewReader(bytes.NewReader(original), int64(len(original)))
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for _, f := range reader.File {
				target, _ := writer.Create(f.Name)
				if f.Name == member {
					io.WriteString(target, replacement)
				} else {
					source, _ := f.Open()
					io.Copy(target, source)
					source.Close()
				}
			}
			writer.Close()
			if _, err := m.VerifyBackup(context.Background(), buffer.Bytes(), "Synthetic restore"); err == nil {
				t.Fatal("business-invalid ZIP accepted")
			}
		})
	}
}

func TestBillingStatusUsesInstanceSwitch(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Synthetic billing")
	if err != nil {
		t.Fatal(err)
	}
	instance, _ := m.lookup(info.ID)
	if err := instance.config.Update("BILLING_ENABLED", "true", false); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	m.serveBillingStatus(response, httptest.NewRequest("GET", "/", nil))
	var value struct{ Instances []struct{ Enabled bool } }
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || len(value.Instances) != 1 || !value.Instances[0].Enabled {
		t.Fatal(response.Body.String(), err)
	}
}

func TestRestartReplacesRunningRuntime(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Synthetic restart")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	instance, _ := m.lookup(info.ID)
	old := instance.runtime
	if err := m.Restart(info.ID); err != nil {
		t.Fatal(err)
	}
	if old == instance.runtime || old.Scope.Context().Err() == nil || instance.runtime.Scope.Context().Err() != nil {
		t.Fatal("restart did not stop and replace active runtime")
	}
}

func TestCopyRemovesEndpointAuthentication(t *testing.T) {
	settings := backup.Settings{ModelRouter: modelrouter.Configuration{Endpoints: []modelrouter.Endpoint{{BaseURL: "https://synthetic:credential@upstream.invalid/v1?token=synthetic-secret#fragment"}}}}
	copied := backup.ForConfigurationCopy(settings)
	data, _ := json.Marshal(copied)
	if copied.ModelRouter.Endpoints[0].BaseURL != "https://upstream.invalid/v1" {
		t.Fatalf("copy retained authentication: %s", data)
	}
}
