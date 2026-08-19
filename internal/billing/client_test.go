package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Balance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/balance" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}

		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["platform"] != "qq" || req["external_id"] != "114514" {
			t.Errorf("unexpected payload: %+v", req)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": BalanceResult{
				Exists:       true,
				Platform:     "qq",
				ExternalID:   "114514",
				UserUID:      "u-123",
				BalanceMinor: 2500,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-secret", 2*time.Second)
	res, err := client.Balance(context.Background(), "qq", "114514")
	if err != nil {
		t.Fatalf("balance failed: %v", err)
	}

	if !res.Exists || res.BalanceMinor != 2500 || res.UserUID != "u-123" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestClient_ReserveLLM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/llm/reservations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Idempotency-Key") != "idemp-key-123" {
			t.Errorf("unexpected Idempotency-Key: %s", r.Header.Get("Idempotency-Key"))
		}

		var req LLMReserveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.AmountMinor != 500 || req.TaskID != "t-1" {
			t.Errorf("unexpected request payload: %+v", req)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": LLMReservationResult{
				ReservationID: "res-001",
				UserUID:       "u-123",
				Decision:      DecisionReserved,
				Status:        StatusReserved,
				ReservedMinor: 500,
				BalanceMinor:  2000,
				Created:       true,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", 2*time.Second)
	res, err := client.ReserveLLM(context.Background(), LLMReserveRequest{
		Platform:       "qq",
		ExternalID:     "114514",
		DisplayName:    "TestUser",
		TaskID:         "t-1",
		CallID:         "c-1",
		AmountMinor:    500,
		IdempotencyKey: "idemp-key-123",
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if res.ReservationID != "res-001" || res.Decision != DecisionReserved || res.ReservedMinor != 500 {
		t.Errorf("unexpected reservation result: %+v", res)
	}
}

func TestClient_CommitLLM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/llm/reservations/res-001/commit" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		var req map[string]int64
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["actual_minor"] != 120 {
			t.Errorf("unexpected actual_minor: %d", req["actual_minor"])
		}

		actual := int64(120)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": LLMReservationResult{
				ReservationID: "res-001",
				UserUID:       "u-123",
				Decision:      DecisionReserved,
				Status:        StatusCommitted,
				ReservedMinor: 500,
				ActualMinor:   &actual,
				BalanceMinor:  2380,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", 2*time.Second)
	res, err := client.CommitLLM(context.Background(), "res-001", 120)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if res.Status != StatusCommitted || *res.ActualMinor != 120 || res.BalanceMinor != 2380 {
		t.Errorf("unexpected commit result: %+v", res)
	}
}

func TestClient_ReleaseLLM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/llm/reservations/res-001/release" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["reason"] != ReasonModelFailed {
			t.Errorf("unexpected reason: %s", req["reason"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": LLMReservationResult{
				ReservationID: "res-001",
				UserUID:       "u-123",
				Decision:      DecisionReserved,
				Status:        StatusReleased,
				ReservedMinor: 500,
				BalanceMinor:  2500,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", 2*time.Second)
	res, err := client.ReleaseLLM(context.Background(), "res-001", ReasonModelFailed)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	if res.Status != StatusReleased || res.BalanceMinor != 2500 {
		t.Errorf("unexpected release result: %+v", res)
	}
}

func TestClient_ErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorCode  string
		wantErr    error
	}{
		{
			name:       "not found",
			statusCode: http.StatusNotFound,
			errorCode:  "reservation_not_found",
			wantErr:    ErrReservationNotFound,
		},
		{
			name:       "idempotency conflict",
			statusCode: http.StatusConflict,
			errorCode:  "idempotency_conflict",
			wantErr:    ErrIdempotencyConflict,
		},
		{
			name:       "reservation expired",
			statusCode: http.StatusConflict,
			errorCode:  "reservation_expired",
			wantErr:    ErrReservationExpired,
		},
		{
			name:       "reservation terminal",
			statusCode: http.StatusConflict,
			errorCode:  "reservation_terminal",
			wantErr:    ErrReservationTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    tt.errorCode,
						"message": "test error",
					},
				})
			}))
			defer server.Close()

			client := NewClient(server.URL, "", time.Second)
			_, err := client.CommitLLM(context.Background(), "res-1", 100)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}
