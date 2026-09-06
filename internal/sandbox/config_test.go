package sandbox_test

import (
	"os"
	"testing"
	"time"

	"FrostAgent/internal/sandbox"
)

func TestConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     sandbox.Config
		wantErr bool
	}{
		{
			name: "disabled sandbox is valid even if empty",
			cfg: sandbox.Config{
				Enabled: false,
			},
			wantErr: false,
		},
		{
			name: "enabled sandbox with valid config",
			cfg: sandbox.Config{
				Enabled:          true,
				BaseURL:          "http://127.0.0.1:13874",
				AuthToken:        "secret-token",
				SessionNamespace: "frostagent",
			},
			wantErr: false,
		},
		{
			name: "enabled with empty base url",
			cfg: sandbox.Config{
				Enabled:          true,
				BaseURL:          "",
				AuthToken:        "secret-token",
				SessionNamespace: "frostagent",
			},
			wantErr: true,
		},
		{
			name: "enabled with invalid base url scheme",
			cfg: sandbox.Config{
				Enabled:          true,
				BaseURL:          "ftp://127.0.0.1:13874",
				AuthToken:        "secret-token",
				SessionNamespace: "frostagent",
			},
			wantErr: true,
		},
		{
			name: "enabled with empty auth token",
			cfg: sandbox.Config{
				Enabled:          true,
				BaseURL:          "http://127.0.0.1:13874",
				AuthToken:        "",
				SessionNamespace: "frostagent",
			},
			wantErr: true,
		},
		{
			name: "enabled with empty namespace",
			cfg: sandbox.Config{
				Enabled:          true,
				BaseURL:          "http://127.0.0.1:13874",
				AuthToken:        "secret-token",
				SessionNamespace: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	// Clear env first
	os.Unsetenv("SANDBOX_ENABLED")
	os.Unsetenv("SANDBOX_BASE_URL")
	os.Unsetenv("SANDBOX_AUTH_TOKEN")
	os.Unsetenv("SANDBOX_SESSION_NAMESPACE")

	cfg := sandbox.LoadConfigFromEnv()
	if cfg.Enabled {
		t.Errorf("expected Enabled to be false by default")
	}
	if cfg.BaseURL != sandbox.DefaultBaseURL {
		t.Errorf("expected BaseURL %s, got %s", sandbox.DefaultBaseURL, cfg.BaseURL)
	}
	if cfg.SessionNamespace != sandbox.DefaultSessionNamespace {
		t.Errorf("expected SessionNamespace %s, got %s", sandbox.DefaultSessionNamespace, cfg.SessionNamespace)
	}

	// Custom env
	os.Setenv("SANDBOX_ENABLED", "true")
	os.Setenv("SANDBOX_BASE_URL", "https://gateway.internal:9999")
	os.Setenv("SANDBOX_AUTH_TOKEN", "custom-token")
	os.Setenv("SANDBOX_SESSION_NAMESPACE", "custom-instance")
	defer func() {
		os.Unsetenv("SANDBOX_ENABLED")
		os.Unsetenv("SANDBOX_BASE_URL")
		os.Unsetenv("SANDBOX_AUTH_TOKEN")
		os.Unsetenv("SANDBOX_SESSION_NAMESPACE")
	}()

	customCfg := sandbox.LoadConfigFromEnv()
	if !customCfg.Enabled {
		t.Errorf("expected Enabled to be true")
	}
	if customCfg.BaseURL != "https://gateway.internal:9999" {
		t.Errorf("expected custom BaseURL, got %s", customCfg.BaseURL)
	}
	if customCfg.AuthToken != "custom-token" {
		t.Errorf("expected custom AuthToken, got %s", customCfg.AuthToken)
	}
	if customCfg.SessionNamespace != "custom-instance" {
		t.Errorf("expected custom SessionNamespace, got %s", customCfg.SessionNamespace)
	}
	if customCfg.ClientTimeout < 120*time.Second {
		t.Errorf("expected client timeout to cover max execution timeout, got %v", customCfg.ClientTimeout)
	}
}
