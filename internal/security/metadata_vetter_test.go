package security

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMetadataVetter_Basic(t *testing.T) {
	vetter := NewMetadataVetter()

	// Empty string is trivially safe and cached
	safe, cached := vetter.Check("")
	if !safe || !cached {
		t.Errorf("expected empty string to be safe and cached, got safe=%v, cached=%v", safe, cached)
	}

	// Uncached value returns false, false
	safe, cached = vetter.Check("syn_user_nick_01")
	if safe || cached {
		t.Errorf("expected uncached string to return false, false, got safe=%v, cached=%v", safe, cached)
	}

	// Mark in-flight
	if !vetter.MarkInFlight("syn_user_nick_01") {
		t.Error("expected first MarkInFlight to return true")
	}
	// Deduplicate in-flight: second call must return false
	if vetter.MarkInFlight("syn_user_nick_01") {
		t.Error("expected second MarkInFlight to return false (already in-flight)")
	}

	// Record safe result
	vetter.Record("syn_user_nick_01", true)
	safe, cached = vetter.Check("syn_user_nick_01")
	if !safe || !cached {
		t.Errorf("expected recorded safe string to return true, true, got safe=%v, cached=%v", safe, cached)
	}

	// After recording, MarkInFlight should return false (already cached)
	if vetter.MarkInFlight("syn_user_nick_01") {
		t.Error("expected MarkInFlight on cached text to return false")
	}

	// Record malicious/unsafe result
	vetter.Record("syn_malicious_card", false)
	safe, cached = vetter.Check("syn_malicious_card")
	if safe || !cached {
		t.Errorf("expected recorded unsafe string to return false, true, got safe=%v, cached=%v", safe, cached)
	}
}

func TestMetadataVetter_ClearInFlight(t *testing.T) {
	vetter := NewMetadataVetter()
	key := "syn_pending_nick"

	if !vetter.MarkInFlight(key) {
		t.Fatal("expected MarkInFlight to succeed")
	}
	if vetter.InFlightCount() != 1 {
		t.Errorf("expected 1 in-flight, got %d", vetter.InFlightCount())
	}

	// Clearing in-flight allows subsequent vetting attempts (e.g. on retry after timeout)
	vetter.ClearInFlight(key)
	if vetter.InFlightCount() != 0 {
		t.Errorf("expected 0 in-flight after clear, got %d", vetter.InFlightCount())
	}
	if !vetter.MarkInFlight(key) {
		t.Error("expected MarkInFlight to succeed after ClearInFlight")
	}
}

func TestMetadataVetter_ConcurrentAccess(t *testing.T) {
	vetter := NewMetadataVetter()
	var wg sync.WaitGroup
	workers := 50
	iterations := 100

	for i := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			key := "syn_worker_key"
			if workerID%2 == 0 {
				key = "syn_alternate_key"
			}
			for j := range iterations {
				switch j % 3 {
				case 0:
					if vetter.MarkInFlight(key) {
						time.Sleep(time.Microsecond)
						vetter.Record(key, workerID%2 == 0)
					}
				case 1:
					vetter.Check(key)
				default:
					vetter.ClearInFlight(key)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestController_MetadataVetterIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := NewController(filepath.Join(tmpDir, "sec"))

	vetter := ctrl.GetMetadataVetter()
	if vetter == nil {
		t.Fatal("expected non-nil MetadataVetter from Controller")
	}

	p, err := NewPrincipal("qq", "syn_usr_ctrl_01")
	if err != nil {
		t.Fatalf("NewPrincipal: %v", err)
	}

	// Verify EvaluateContextWithContext adheres to context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel context

	dec := ctrl.EvaluateContextWithContext(ctx, p, SourcePlatformMeta, "syn_test_meta", AuditEvent{})
	// In simple mode (default), semantic evaluation is bypassed and passes
	if dec.Action != WatchdogPass {
		t.Errorf("expected WatchdogPass in simple mode, got %v", dec.Action)
	}

	// In aggressive mode, cancelled context fails-closed with IsFailure = true
	ctrl.SetMode(ControlModeAggressive)
	dec = ctrl.EvaluateContextWithContext(ctx, p, SourcePlatformMeta, "syn_test_meta", AuditEvent{})
	if dec.Action != WatchdogBlock || !dec.IsFailure {
		t.Errorf("expected WatchdogBlock with IsFailure on cancelled context in aggressive mode, got %+v", dec)
	}
}
