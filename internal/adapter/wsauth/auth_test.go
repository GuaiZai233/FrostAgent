package wsauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInboundAuthorization(t *testing.T) {
	const secret = "synthetic-inbound-secret"
	for _, tc := range []struct {
		name     string
		token    string
		headers  []string
		required bool
		allowed  bool
	}{
		{"legacy unset", "", nil, false, true},
		{"required unset", "", nil, true, false},
		{"required unset supplied", "", []string{"Bearer " + secret}, true, false},
		{"missing", secret, nil, false, false},
		{"incorrect", secret, []string{"Bearer synthetic-wrong"}, false, false},
		{"truncated", secret, []string{"Bearer synthetic"}, false, false},
		{"valid", secret, []string{"Bearer " + secret}, false, true},
		{"required valid", secret, []string{"Bearer " + secret}, true, true},
		{"case insensitive scheme", secret, []string{"bearer " + secret}, false, true},
		{"wrong scheme", secret, []string{"Basic " + secret}, false, false},
		{"extra whitespace", secret, []string{"Bearer  " + secret}, false, false},
		{"duplicate", secret, []string{"Bearer " + secret, "Bearer " + secret}, false, false},
		{"joined", secret, []string{"Bearer " + secret + ", Bearer " + secret}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Query parameters, browser subprotocols and control-plane headers
			// must never substitute for the inbound Authorization header.
			r := httptest.NewRequest(http.MethodGet, "/ws?token="+secret+"&access_token="+secret, nil)
			r.Header["Authorization"] = tc.headers
			r.Header.Set("X-MCP-Control-Token", secret)
			r.Header.Set("Sec-WebSocket-Protocol", secret)
			if tc.required {
				r = RequireToken(r)
			}
			w := httptest.NewRecorder()
			if Authorize(w, r, tc.token) != tc.allowed {
				t.Fatal("unexpected authorization decision")
			}
			if !tc.allowed && (w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("denial must be a non-cacheable 401")
			}
			if strings.Contains(w.Body.String()+fmt.Sprint(w.Header()), secret) {
				t.Fatal("response disclosed a credential")
			}
		})
	}
}
