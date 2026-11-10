package botstatus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func TestTestBalanceQueryReturnsActualResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"quota":92275,"used_quota":1000}`))
	}))
	defer server.Close()
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	t.Cleanup(scope.Cancel)
	service := New(&llm.Engine{Scope: scope}, "test", "")
	response, err := service.TestBalanceQueryConfig(context.Background(), connect.NewRequest(&v1.TestBalanceQueryConfigRequest{
		Curl:         "curl " + server.URL + "/balance -H 'Authorization: Bearer {{API_KEY}}'",
		QuotaPerUnit: 500000,
	}))
	if err != nil || !response.Msg.Ok {
		t.Fatalf("test connection failed: %v, %v", response, err)
	}
	if !strings.Contains(response.Msg.Message, `"quota":"0.18455 USD"`) {
		t.Fatalf("success message must carry the converted response body, got %q", response.Msg.Message)
	}
}
