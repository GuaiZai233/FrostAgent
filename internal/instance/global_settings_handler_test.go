package instance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	pbconnect "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/GuaiZai233/FrostAgent/internal/managementauth"
	"github.com/GuaiZai233/FrostAgent/internal/testutil/sqlfault"
)

func TestSettingsCommitLossAppliesReadbackBeforeResponse(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "Synthetic commit loss")
	previous := m.instances[info.ID].runtime
	applied := false
	m.SetGlobalApplyHook(func() error {
		applied = m.global.Get("HTTP_ALLOWED_ORIGINS") == "https://synthetic.invalid"
		return nil
	})
	sqlfault.Install(t, m.db, filepath.Join(m.root, "frostagent.db"))
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"AGENT_MAX_ITERATIONS","value":"23"},{"key":"HTTP_ALLOWED_ORIGINS","value":"https://synthetic.invalid"}]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"unknown"`) {
		t.Fatalf("lost acknowledgement was not unknown: %d %s", response.Code, response.Body)
	}
	if !applied || m.instances[info.ID].runtime == previous || m.instances[info.ID].runtime.Scope.Getenv("AGENT_MAX_ITERATIONS") != "23" {
		t.Fatal("readback bypassed runtime or global application")
	}
}

func TestSettingsCompensationPreservesUnknown(t *testing.T) {
	for _, scope := range []string{"global", "instance"} {
		for _, failAt := range []int32{1, 2} {
			t.Run(scope+string(rune('0'+failAt)), func(t *testing.T) {
				m := testManager(t)
				info := create(t, m, "Synthetic compensation")
				calls := 0
				m.SetGlobalApplyHook(func() error {
					calls++
					if calls == 1 {
						return errors.New("synthetic runtime application failure")
					}
					return nil
				})
				fault := sqlfault.Install(t, m.db, filepath.Join(m.root, "frostagent.db"))
				fault.FailAt = failAt
				body := `{"updates":[{"key":"HTTP_ALLOWED_ORIGINS","value":"https://synthetic.invalid"}]}`
				var response *httptest.ResponseRecorder
				if scope == "instance" {
					response = applySettingsDraft(m, info.ID, body)
				} else {
					request := localSettingsRequest(http.MethodPost, pbconnect.SettingsServiceUpdateEnvVarsProcedure, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					response = httptest.NewRecorder()
					m.ServeHTTP(response, request)
				}
				if response.Code < 500 || scope == "instance" && !strings.Contains(response.Body.String(), `"state":"unknown"`) {
					t.Fatalf("unknown became a definite failure: %d %s", response.Code, response.Body)
				}
				if fault.Commits.Load() != 2 {
					t.Fatalf("did not exercise write and compensation: %d", fault.Commits.Load())
				}
			})
		}
	}
}

func TestGlobalSettingsRPCPreservesCompressedProtocols(t *testing.T) {
	m := testManager(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.ServeHTTP(w, r.WithContext(managementauth.WithLocalAdmin(r.Context())))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name string
		opts []connect.ClientOption
	}{
		{"connect-json", []connect.ClientOption{connect.WithProtoJSON()}},
		{"connect-proto", nil},
		{"grpc-json", []connect.ClientOption{connect.WithGRPC(), connect.WithProtoJSON()}},
		{"grpc-proto", []connect.ClientOption{connect.WithGRPC()}},
		{"grpc-web-json", []connect.ClientOption{connect.WithGRPCWeb(), connect.WithProtoJSON()}},
		{"grpc-web-proto", []connect.ClientOption{connect.WithGRPCWeb()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := pbconnect.NewSettingsServiceClient(server.Client(), server.URL, append(tc.opts, connect.WithSendGzip())...)
			key := strings.Repeat("<synthetic>&", 500)
			response, err := client.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{
				Scope: "global", Updates: []*v1.EnvVar{{Key: key, Value: "ignored"}},
			}))
			if err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Msg.Success || len(response.Msg.Errors) != 1 || response.Msg.Errors[0].Key != key {
				t.Fatal("validation response was changed in transit")
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing nosniff header")
			}
			if strings.HasPrefix(tc.name, "connect-") && response.Header().Get("Content-Encoding") != "gzip" {
				t.Fatal("fixture did not exercise compressed response")
			}
		})
	}
}

func TestGlobalSettingsApplyFailureRollsBackBeforeRPCSuccess(t *testing.T) {
	m := testManager(t)
	before := m.global.Get("HTTP_ALLOWED_ORIGINS")
	calls := 0
	m.SetGlobalApplyHook(func() error {
		calls++
		if calls == 1 {
			return errors.New("synthetic listener failure")
		}
		return nil
	})
	request := localSettingsRequest(http.MethodPost, pbconnect.SettingsServiceUpdateEnvVarsProcedure,
		strings.NewReader(`{"scope":"global","updates":[{"key":"HTTP_ALLOWED_ORIGINS","value":"https://synthetic.invalid"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code == http.StatusOK || m.global.Get("HTTP_ALLOWED_ORIGINS") != before || calls != 2 {
		t.Fatalf("apply failure did not roll back before response: code=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}
