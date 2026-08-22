package llm

import (
	"testing"
)

func TestDefaultGroupRawContextConfig(t *testing.T) {
	cfg := DefaultGroupRawContextConfig()
	if cfg.MaxChars != DefaultGroupRawContextMaxChars {
		t.Errorf("expected MaxChars = %d, got %d", DefaultGroupRawContextMaxChars, cfg.MaxChars)
	}
}

func TestLoadGroupRawContextConfigFromEnv_Dynamic(t *testing.T) {
	// Case 1: default when env unset
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "")
	cfg := LoadGroupRawContextConfigFromEnv()
	if cfg.MaxChars != DefaultGroupRawContextMaxChars {
		t.Errorf("expected default %d, got %d", DefaultGroupRawContextMaxChars, cfg.MaxChars)
	}

	// Case 2: valid custom value
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "8000")
	cfg = LoadGroupRawContextConfigFromEnv()
	if cfg.MaxChars != 8000 {
		t.Errorf("expected 8000, got %d", cfg.MaxChars)
	}

	// Case 3: hot reload back to another value
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "15000")
	cfg = LoadGroupRawContextConfigFromEnv()
	if cfg.MaxChars != 15000 {
		t.Errorf("expected 15000, got %d", cfg.MaxChars)
	}

	// Case 4: invalid format fallbacks to default
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "invalid_chars")
	cfg = LoadGroupRawContextConfigFromEnv()
	if cfg.MaxChars != DefaultGroupRawContextMaxChars {
		t.Errorf("expected fallback %d, got %d", DefaultGroupRawContextMaxChars, cfg.MaxChars)
	}

	// Case 5: non-positive number fallbacks to default
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "-100")
	cfg = LoadGroupRawContextConfigFromEnv()
	if cfg.MaxChars != DefaultGroupRawContextMaxChars {
		t.Errorf("expected fallback %d, got %d", DefaultGroupRawContextMaxChars, cfg.MaxChars)
	}
}

func TestEngineGroupRawLimitAndMaxChars(t *testing.T) {
	t.Setenv("GROUP_RAW_CONTEXT_MAX_CHARS", "5000")

	// Nil engine
	var nilEngine *Engine
	if limit := nilEngine.GroupRawLimit(); limit != DefaultGroupCompactBufferSize {
		t.Errorf("expected default limit %d for nil engine, got %d", DefaultGroupCompactBufferSize, limit)
	}
	if maxChars := nilEngine.GroupRawMaxChars(); maxChars != 5000 {
		t.Errorf("expected 5000 maxChars for nil engine with env set, got %d", maxChars)
	}

	// Engine with nil GroupCompactor
	e := &Engine{}
	if limit := e.GroupRawLimit(); limit != DefaultGroupCompactBufferSize {
		t.Errorf("expected default limit %d for engine with nil GroupCompactor, got %d", DefaultGroupCompactBufferSize, limit)
	}

	// Engine with GroupCompactor
	compactor := &GroupCompactor{bufferSize: 35}
	e.GroupCompactor = compactor
	if limit := e.GroupRawLimit(); limit != 35 {
		t.Errorf("expected limit 35 matching GroupCompactor BufferSize, got %d", limit)
	}
}
