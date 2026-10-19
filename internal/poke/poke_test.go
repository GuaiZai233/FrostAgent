package poke

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateConcurrentAndCooldown(t *testing.T) {
	var gate Gate
	now := time.Now()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			if gate.Allow("bot:group:a", now, 30*time.Second) {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("allowed %d concurrent wakes", allowed.Load())
	}
	if gate.Allow("bot:group:a", now.Add(29*time.Second), 30*time.Second) {
		t.Fatal("allowed early wake")
	}
	if !gate.Allow("bot:group:a", now.Add(30*time.Second), 30*time.Second) {
		t.Fatal("boundary should expire")
	}
	for _, key := range []string{"bot:group:b", "bot:private:a", "other-bot:group:a"} {
		if !gate.Allow(key, now, 30*time.Second) {
			t.Fatalf("cross-conversation cooldown: %s", key)
		}
	}
	if !gate.Allow("bot:group:a", now.Add(31*time.Second), 0) {
		t.Fatal("zero must disable cooldown")
	}
	var other Gate
	if !other.Allow("bot:group:a", now, 30*time.Second) {
		t.Fatal("runtime isolation lost")
	}
	gate.Allow("new", now.Add(25*time.Hour), time.Second)
	if len(gate.last) != 1 {
		t.Fatalf("expired entries retained: %v", gate.last)
	}
}
func TestPokeMessageAndCooldownDefaults(t *testing.T) {
	for input, want := range map[string]string{"": "（用户戳了戳你）", "  ": "（用户戳了戳你）", " 的耳朵 ": "（用户戳了戳你 的耳朵）"} {
		if got := Message(input); got != want {
			t.Fatalf("Message(%q)=%q", input, got)
		}
	}
	for _, test := range []struct {
		raw     string
		seconds int
	}{{"", 30}, {"bad", 30}, {"-1", 30}, {"86401", 30}, {"0", 0}, {"15", 15}} {
		if got := Cooldown(func(string) string { return test.raw }); got != time.Duration(test.seconds)*time.Second {
			t.Fatalf("cooldown %q = %v", test.raw, got)
		}
	}
	if ID() == ID() {
		t.Fatal("poke task identifiers collided")
	}
}
