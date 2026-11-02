package instance

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComputerUseTakeoverAdmittedWhileBridgeRequestWaits(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "synthetic-computer-use")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.instances[info.ID].runtime.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/computer-use/projects" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer func() { close(release); <-done }()
	go func() {
		defer close(done)
		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/instances/"+info.ID+"/api/computer-use/projects", nil))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never entered runtime")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/instances/"+info.ID+"/api/computer-use/takeover", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("takeover blocked by ordinary bridge request: %d %s", w.Code, w.Body.String())
	}
}
