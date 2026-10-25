package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestEmbeddingWireAndResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"reordered", `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`, true},
		{"duplicate", `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[1,0]}]}`, false},
		{"missing", `{"data":[{"index":0,"embedding":[1,0]}]}`, false},
		{"dimension", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1]}]}`, false},
		{"zero", `{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[1,0]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" || r.Method != http.MethodPost {
					t.Errorf("wrong embedding endpoint: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("embedding credential missing")
				}
				var wire map[string]any
				_ = json.NewDecoder(r.Body).Decode(&wire)
				if wire["model"] != "embed-test" || wire["encoding_format"] != "float" || wire["route"] != nil || wire["messages"] != nil {
					t.Errorf("wrong wire format: %v", wire)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := NewClient(server.URL+"/v1", "synthetic-key").Embed(context.Background(), core.EmbeddingRequest{Model: "embed-test", Input: []string{"a", "b"}, Route: core.RouteContext{GroupID: "synthetic-group"}})
			if tc.valid {
				if err != nil || result.Vectors[0][0] != 1 || result.Vectors[1][1] != 1 {
					t.Fatalf("ordered result: %v, %v", result, err)
				}
			} else if err == nil {
				t.Fatal("malformed vectors accepted")
			}
		})
	}
}

func TestEmbeddingExplicitLengthRejection(t *testing.T) {
	if !IsInputTooLong(&HTTPError{StatusCode: 400, Body: `{"code":"context_length_exceeded"}`}) {
		t.Fatal("length rejection not recognized")
	}
	if IsInputTooLong(&HTTPError{StatusCode: 401, Body: "maximum context"}) {
		t.Fatal("authorization failure classified as input length")
	}
}
