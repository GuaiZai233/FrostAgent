package instance

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
)

func TestRuntimeAndOverviewShareBuildVersion(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "build-version-fixture")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	want := buildinfo.DisplayVersion()
	item := m.instances[info.ID]
	item.op.Lock()
	got := item.runtime.Engine.Version
	item.op.Unlock()
	if got != want {
		t.Fatalf("engine version = %q, want build identity %q", got, want)
	}
	response := rpc(t, m, info.ID, "BotStatusService/GetOverview", "{}", false)
	if response.Code != http.StatusOK {
		t.Fatalf("overview: status=%d body=%s", response.Code, response.Body.String())
	}
	var overview struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Version != want {
		t.Fatalf("overview version = %q, want build identity %q", overview.Version, want)
	}
}
