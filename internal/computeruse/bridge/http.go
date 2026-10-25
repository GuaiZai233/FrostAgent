package bridge

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
)

var bridgeToken = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

// NewHandler requires direct TLS and a high-entropy base64url bearer token.
// The token delegates this dedicated VM to FrostAgent, not to arbitrary chat
// users. Owner authorization must happen before FrostAgent calls this boundary.
// Deploy only on a private network; never mount this on the adapter listener.
func NewHandler(service *Service, token string) (http.Handler, error) {
	if service == nil || !bridgeToken.MatchString(token) {
		return nil, ErrInvalid
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/operations/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.content(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	})
	mux.HandleFunc("PUT /v1/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, ErrInvalid)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request Request
		if decoder.Decode(&request) != nil || request.OperationID != r.PathValue("id") {
			writeError(w, ErrInvalid)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			writeError(w, ErrInvalid)
			return
		}
		op, err := service.Submit(r.Context(), request)
		writeOperation(w, op, err)
	})
	mux.HandleFunc("GET /v1/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		op, err := service.Lookup(r.PathValue("id"))
		writeOperation(w, op, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.TLS == nil {
			writeError(w, ErrUnauthorized)
			return
		}
		provided := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
			writeError(w, ErrUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func writeOperation(w http.ResponseWriter, op Operation, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(op)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "journal_unavailable"
	switch {
	case errors.Is(err, ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "operation_conflict"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrContentExpired):
		status, code = http.StatusGone, "content_expired"
	case errors.Is(err, ErrCapacity):
		status, code = http.StatusTooManyRequests, "capacity"
	case errors.Is(err, ErrClosed):
		code = "stopping"
	case errors.Is(err, ErrAudit):
		code = "audit_unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}
