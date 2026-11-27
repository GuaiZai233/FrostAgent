package frostctl

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	pbconnect "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	settings "github.com/GuaiZai233/FrostAgent/internal/service/settings"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/testutil/sqlfault"
)

func TestConfigCommitAcknowledgementLoss(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "readback"
		if unavailable {
			name = "unavailable"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			db, err := storage.Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			global, err := instanceconfig.OpenDatabase(db, "", true)
			if err != nil {
				t.Fatal(err)
			}
			service := settings.NewScoped(nil, global, nil)
			_, handler := pbconnect.NewSettingsServiceHandler(service)
			server := httptest.NewServer(handler)
			defer server.Close()
			c := &client{base: server.URL, http: server.Client()}
			fault := sqlfault.Install(t, db, filepath.Join(root, "frostagent.db"))
			fault.FailReadback = unavailable
			inv, _ := Parse([]string{"config", "set", "HTTP_ALLOWED_ORIGINS", "--value", "https://synthetic.invalid", "--scope", "global"})
			_, err = execute(context.Background(), c, inv, nil, io.Discard)
			var failure *commandError
			if !errors.As(err, &failure) || failure.Status != "unknown" || fault.Commits.Load() != 1 {
				t.Fatalf("CLI result = %v, commits = %d", err, fault.Commits.Load())
			}
			get, _ := Parse([]string{"config", "get", "HTTP_ALLOWED_ORIGINS", "--scope", "global"})
			data, err := execute(context.Background(), c, get, nil, io.Discard)
			if !unavailable {
				if err != nil || field(data, "value") != "https://synthetic.invalid" {
					t.Fatalf("verification did not read committed value: %v %v", data, err)
				}
				return
			}
			if err == nil {
				t.Fatal("verification reported stale configuration")
			}
			file := filepath.Join(t.TempDir(), "config.json")
			export, _ := Parse([]string{"config", "export", "--scope", "global", "--file", file})
			if _, err := execute(context.Background(), c, export, nil, io.Discard); err == nil {
				t.Fatal("export reported stale configuration")
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("failed export left a misleading file")
			}
			if _, err := service.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: "HTTP_ALLOWED_ORIGINS", Value: ""})); connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("legacy write did not reject unavailable state: %v", err)
			}
			if _, err := service.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: "HTTP_ALLOWED_ORIGINS"})); connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("legacy delete did not reject unavailable state: %v", err)
			}
			if _, err := execute(context.Background(), c, inv, nil, io.Discard); !errors.As(err, &failure) || failure.Status != "failed" || fault.Commits.Load() != 1 {
				t.Fatalf("unconfirmed state allowed another write: %v", err)
			}
		})
	}
}

func TestConfigExportRoundTripAgainstSQLService(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := "synthetic-instance"
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: "Synthetic", CreatedAt: time.Now()}, 1); err != nil {
		t.Fatal(err)
	}
	if err := instanceconfig.InitializeInstanceSettings(db, id); err != nil {
		t.Fatal(err)
	}
	instance, _ := instanceconfig.OpenDatabase(db, id, false)
	global, _ := instanceconfig.OpenDatabase(db, "", true)
	if err := instance.Update("ONEBOT_WS_TOKEN", "synthetic-secret", false); err != nil {
		t.Fatal(err)
	}
	if err := global.Update("SECURITY_GATEWAY_TIMEOUT", "27s", false); err != nil {
		t.Fatal(err)
	}
	service := settings.NewScoped(instance, global, nil)
	_, handler := pbconnect.NewSettingsServiceHandler(service)
	server := httptest.NewServer(http.StripPrefix("/instances/"+id, handler))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	file := filepath.Join(t.TempDir(), "config.json")
	inv, err := Parse([]string{"config", "export", "--instance", id, "--file", file})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(ctx, c, inv, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if strings.Contains(string(raw), "synthetic-secret") || strings.Contains(string(raw), "27s") {
		t.Fatal("export contained secret or inherited override")
	}
	before := instance.Snapshot()
	for _, command := range []string{"validate", "diff", "apply"} {
		inv, _ := Parse([]string{"config", command, "--instance", id, "--file", file})
		if _, err := execute(ctx, c, inv, strings.NewReader(""), io.Discard); err != nil {
			t.Fatalf("%s failed: %v", command, err)
		}
	}
	after := instance.Snapshot()
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("export roundtrip changed SQL configuration")
	}
	inv, _ = Parse([]string{"config", "get", "BALANCE_QUERY_CONFIG", "--instance", id})
	data, err := execute(ctx, c, inv, nil, io.Discard)
	if err != nil || field(data, "key") != "BALANCE_QUERY_CONFIG" || field(data, "value") != "" {
		t.Fatal("secret state is not queryable", data, err)
	}
}

func TestUnknownWriteIncludesUsableVerificationAndCorrelation(t *testing.T) {
	var committed atomic.Bool
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "UpdateEnvVars"):
			writes.Add(1)
			committed.Store(true)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		case strings.HasSuffix(r.URL.Path, "ListEnvVars"):
			if !committed.Load() {
				t.Error("query happened before write")
			}
			json.NewEncoder(w).Encode(map[string]any{"envVars": []any{map[string]any{"key": "BOT_NAME", "value": "Synthetic"}}})
		default:
			w.WriteHeader(503)
			io.WriteString(w, `{"state":"unknown","operationId":"synthetic-operation"}`)
		}
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	inv, _ := Parse([]string{"config", "set", "BOT_NAME", "--value", "Synthetic", "--instance", "synthetic"})
	_, err := execute(context.Background(), c, inv, nil, io.Discard)
	var failure *commandError
	if !errors.As(err, &failure) || failure.Status != "unknown" || writes.Load() != 1 {
		t.Fatal(err)
	}
	verify, err := Parse(failure.Verification[1:])
	if err != nil {
		t.Fatalf("invalid verification argv: %v", err)
	}
	if _, err := execute(context.Background(), c, verify, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	_, err = c.call(context.Background(), "POST", "/unavailable", nil, true)
	if !errors.As(err, &failure) || failure.Correlation["operationId"] != "synthetic-operation" {
		t.Fatal("server correlation lost", err)
	}
}

func TestSupportZIPUsesFilteredMetadataAndMarksPartialFailure(t *testing.T) {
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := any(map[string]any{})
		switch {
		case r.URL.Path == "/api/instances":
			value = map[string]any{"instances": []any{}}
		case strings.HasSuffix(r.URL.Path, "ListEnvVars"):
			value = map[string]any{"envVars": []any{map[string]any{"key": "ADMIN_TOKEN", "value": "synthetic-secret", "secretConfigured": true}}}
		case strings.HasSuffix(r.URL.Path, "ListLogs"):
			var request map[string]any
			json.NewDecoder(r.Body).Decode(&request)
			if request["since"] != "2026-01-01T00:00:00Z" {
				t.Error("since filter omitted")
			}
			if failed {
				w.WriteHeader(503)
				return
			}
			value = map[string]any{"entries": []any{map[string]any{"id": "synthetic-log", "summary": "synthetic-secret", "requestBody": "synthetic-secret", "level": "LOG_LEVEL_ERROR"}}}
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	for _, partial := range []bool{false, true} {
		failed = partial
		file := filepath.Join(t.TempDir(), "support.zip")
		inv, _ := Parse([]string{"support", "collect", "--since", "2026-01-01T00:00:00Z", "--file", file})
		data, err := executeSupport(context.Background(), c, inv)
		if (err != nil) != partial || object(data)["complete"] == partial {
			t.Fatal(data, err)
		}
		raw, _ := os.ReadFile(file)
		archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		logsFound := false
		for _, file := range archive.File {
			reader, _ := file.Open()
			raw, _ := io.ReadAll(reader)
			reader.Close()
			if bytes.Contains(raw, []byte("synthetic-secret")) {
				t.Fatal("support archive disclosed credential")
			}
			logsFound = logsFound || bytes.Contains(raw, []byte("synthetic-log"))
		}
		if !partial && !logsFound {
			t.Fatal("support archive omitted logs")
		}
	}
}

func TestEveryWriteRecoveryCommandAndRequiredFlag(t *testing.T) {
	for _, command := range Commands() {
		if command.Effect != "write" {
			continue
		}
		args := strings.Fields(command.Name)
		for range command.Arguments {
			args = append(args, "synthetic")
		}
		switch command.Scope {
		case "config", "instance":
			args = append(args, "--instance", "synthetic")
		case "global":
			args = append(args, "--scope", "global")
		case "logs":
			args = append(args, "--scope", "control-plane")
		}
		for _, flag := range command.Flags {
			if flag.Required {
				args = append(args, "--"+flag.Name)
				if flag.Type != "bool" {
					args = append(args, "synthetic")
				}
			}
		}
		if command.Name == "backup restore" {
			args = append(args, "--confirm")
		}
		inv, err := Parse(args)
		if err != nil {
			t.Fatalf("%s: %v", command.Name, err)
		}
		recovery := verificationCommand(inv)
		if _, err := Parse(recovery[1:]); err != nil {
			t.Fatalf("%s recovery: %v %v", command.Name, recovery, err)
		}
	}
	if _, err := Parse([]string{"version", "--remote", "false"}); err == nil {
		t.Fatal("alias false bypassed server selection")
	}
}

func TestSessionListHasNoPositionalResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "GetSessions") {
			t.Error(r.URL.Path)
		}
		io.WriteString(w, `{"sessions":[]}`)
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	inv, err := Parse([]string{"session", "list", "--instance", "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(context.Background(), c, inv, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointReadRejectsStaleState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"active":{"revision":"3"},"loadError":"synthetic database failure"}`)
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	inv, _ := Parse([]string{"model", "endpoint", "list", "--scope", "global"})
	if _, err := execute(context.Background(), c, inv, nil, io.Discard); err == nil {
		t.Fatal("stale model snapshot presented as current")
	}
}

func TestMalformedObjectFilesFailBeforeRequest(t *testing.T) {
	for _, body := range []string{"null", "[]", `{} {}`} {
		file := filepath.Join(t.TempDir(), "input.json")
		os.WriteFile(file, []byte(body), 0600)
		for _, args := range [][]string{{"model", "route", "set", "dialogue"}, {"mcp", "server", "update", "synthetic"}} {
			args = append(args, "--instance", "synthetic", "--file", file)
			inv, _ := Parse(args)
			// A nil HTTP client proves that invalid input is rejected before transport.
			if _, err := execute(context.Background(), &client{}, inv, nil, io.Discard); err == nil {
				t.Fatalf("accepted %q", body)
			}
		}
	}
}

func TestDeletionStopsWhenBackupVerificationRejects(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/instances/verify" {
			t.Errorf("unexpected deletion request: %s", r.URL.Path)
		}
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid backup content"}`)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "bad.zip")
	os.WriteFile(file, []byte("synthetic invalid archive"), 0600)
	inv, _ := Parse([]string{"instance", "delete", "confirm", "synthetic", "--backup", file, "--confirm"})
	if _, err := execute(context.Background(), &client{base: server.URL, http: server.Client()}, inv, nil, io.Discard); err == nil || calls != 1 {
		t.Fatal("deletion bypassed verification", err, calls)
	}
}
