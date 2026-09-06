package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdapterListenerDoesNotExposeManagement(t *testing.T) {
	handler := instanceWebSocketHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/instances/a1b2c3d4/ws/onebot", 204},
		{"/instances/a1b2c3d4/ws/astrbot", 204},
		{"/instances/a1b2c3d4/frostagent.v1.SettingsService/GetEnvVars", 404},
		{"/instances/a1b2c3d4/api/sticker/example/image", 404},
		{"/api/instances", 404},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", test.path, nil))
		if w.Code != test.status {
			t.Errorf("%s: got %d, want %d", test.path, w.Code, test.status)
		}
	}
}
