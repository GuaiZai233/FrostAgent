package modelrouter

import (
	"fmt"
	"strings"
	"testing"
)

func TestCredentialErrorsOmitRawConfiguration(t *testing.T) {
	const syntheticValue = "SYNTHETIC_PRIVATE_CREDENTIAL_METADATA"
	backend := &SecretBackend{getenv: func(string) string { return "" }}
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "missing environment variable",
			run: func() error {
				_, err := backend.Resolve(Endpoint{APIKeySource: APIKeyStorageEnv, APIKeyRef: syntheticValue})
				return err
			},
			want: "环境变量",
		},
		{
			name: "unknown source while resolving",
			run: func() error {
				_, err := backend.Resolve(Endpoint{APIKeySource: APIKeyStorage(syntheticValue)})
				return err
			},
			want: "未知 Secret 来源",
		},
		{
			name: "unknown source while validating",
			run: func() error {
				cfg := lifecycleConfiguration()
				cfg.Endpoints[0].APIKeySource = APIKeyStorage(syntheticValue)
				return validateConfiguration(cfg)
			},
			want: "unknown api key source",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected diagnostic category %q, got %v", tt.want, err)
			}
			// Callers wrap these errors before forwarding them to logs and adapters.
			message := fmt.Errorf("resolve model credentials: %w", err).Error()
			if strings.Contains(message, syntheticValue) {
				t.Fatal("credential error exposes raw credential configuration")
			}
		})
	}
}

func TestEnvironmentCredentialResolutionKeepsReference(t *testing.T) {
	const ref = "SYNTHETIC_CREDENTIAL_ENV"
	const value = "synthetic-credential-value"
	backend := &SecretBackend{getenv: func(name string) string {
		if name != ref {
			t.Fatalf("unexpected environment reference %q", name)
		}
		return " " + value + " "
	}}
	key, err := backend.Resolve(Endpoint{APIKeySource: APIKeyStorageEnv, APIKeyRef: " " + ref + " "})
	if err != nil || key != value {
		t.Fatalf("environment credential resolution changed: %v", err)
	}
}
