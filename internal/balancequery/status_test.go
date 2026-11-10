package balancequery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchQuotaStatusReadsConversionSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000,"usd_exchange_rate":7.3,"quota_display_type":"USD"}}`))
	}))
	defer server.Close()
	status, err := FetchQuotaStatus(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status.QuotaPerUnit != 500000 || status.USDExchangeRate != 7.3 || status.QuotaDisplayType != "USD" {
		t.Fatalf("unexpected status %+v", status)
	}
}

func TestFetchQuotaStatusRejectsInvalidAndRedirects(t *testing.T) {
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":0}}`))
	}))
	defer invalid.Close()
	if _, err := FetchQuotaStatus(context.Background(), invalid.URL); err == nil {
		t.Fatal("non-positive quota_per_unit must be rejected")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := FetchQuotaStatus(context.Background(), redirect.URL); err == nil {
		t.Fatal("redirects must not be followed")
	}
}

func TestOriginOfDropsPath(t *testing.T) {
	origin, err := OriginOf("curl https://newapi.example/v1/api/usage/token/ -H 'Authorization: Bearer {{API_KEY}}'")
	if err != nil || origin != "https://newapi.example" {
		t.Fatalf("OriginOf() = %q, %v", origin, err)
	}
}
