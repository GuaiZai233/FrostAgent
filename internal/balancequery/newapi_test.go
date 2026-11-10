package balancequery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateNewAPIPayload(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"account self payload", `{"success":true,"data":{"quota":92275,"used_quota":1}}`, true},
		{"token usage payload", `{"object":"token_usage","total_available":92275}`, true},
		{"empty body", ``, false},
		{"success with empty data", `{"success":true,"data":null}`, false},
		{"success with unrelated data", `{"success":true,"data":{"email":"x"}}`, false},
		{"explicit failure", `{"success":false,"message":"invalid token"}`, false},
		{"not json", `<html>login</html>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNewAPIPayload(tc.body)
			if (err == nil) != tc.ok {
				t.Fatalf("validateNewAPIPayload(%q) error = %v, want ok=%v", tc.body, err, tc.ok)
			}
		})
	}
}

func TestNewAPIQueryRejectsEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":null}`))
	}))
	defer server.Close()
	cfg := Config{Curl: "curl " + server.URL + "/api/user/self -H 'Authorization: Bearer {{API_KEY}}'", APIKey: "synthetic-newapi-key", NewAPI: true}
	_, err := Execute(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "额度字段") {
		t.Fatalf("empty New API success must be rejected, got %v", err)
	}
	cfg.NewAPI = false
	if _, err := Execute(context.Background(), cfg); err != nil {
		t.Fatalf("non New API templates keep the raw body behaviour: %v", err)
	}
}

func TestConvertQuotaHandlesNestedAccountData(t *testing.T) {
	got := ConvertQuota(`{"success":true,"data":{"quota":92275,"used_quota":1000}}`, DefaultQuotaPerUnit)
	want := `{"data":{"quota":"0.18455 Dollar","used_quota":"0.002 Dollar"},"success":true}`
	if got != want {
		t.Fatalf("ConvertQuota() = %s, want %s", got, want)
	}
}
