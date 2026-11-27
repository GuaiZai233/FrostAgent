package managementauth

import (
	"context"
	"net/http"
	"testing"
)

func TestHTTPAndIPCIdentityBoundaries(t *testing.T) {
	for _, configured := range []string{"", "synthetic-token"} {
		for _, supplied := range []string{"", "Bearer wrong", "Bearer synthetic-token"} {
			header := http.Header{"Authorization": []string{supplied}, "X-FrostAgent-Local-Admin": []string{"true"}}
			get := func(key string) string {
				if key == "MCP_CONTROL_TOKEN" {
					return configured
				}
				return "true"
			}
			ctx, err := Authenticate(context.Background(), header, get)
			if (err == nil) != (configured != "" && supplied == "Bearer "+configured) {
				t.Fatalf("token boundary: configured=%t supplied=%t", configured != "", supplied != "")
			}
			if err == nil && (!Authenticated(ctx) || LocalAdmin(ctx)) {
				t.Fatal("HTTP principal became local admin")
			}
			local, err := Authenticate(WithLocalAdmin(context.Background()), header, get)
			if err != nil || !LocalAdmin(local) {
				t.Fatal("OS-authenticated IPC depended on HTTP credentials")
			}
		}
	}
}
func TestTokenPrincipalRecheckedOnDatabaseSwitch(t *testing.T) {
	header := http.Header{"Authorization": []string{"Bearer synthetic-a"}}
	ctx, err := Authenticate(context.Background(), header, func(string) string { return "synthetic-a" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticate(ctx, header, func(string) string { return "synthetic-b" }); err == nil {
		t.Fatal("stale HTTP principal bypassed rotated token")
	}
}
