package llm

import "testing"

func TestSessionBotLoopStreak(t *testing.T) {
	s := &SessionContext{}
	observe := func(sender string, marked, eligible bool, revision string) bool {
		return s.ObserveBotLoop(sender, marked, eligible, 2, revision)
	}
	if observe("bot", true, true, "v1") || observe("bot", true, true, "v1") {
		t.Fatal("first two bot turns must be allowed")
	}
	if !observe("bot", true, true, "v1") {
		t.Fatal("third consecutive bot turn must exceed limit")
	}
	if observe("bot", true, false, "v1") {
		t.Fatal("unwoken bot message is not a request")
	}
	observe("human", false, false, "v1")
	if observe("bot", true, true, "v1") {
		t.Fatal("unwoken human message must interrupt the streak")
	}
	if observe("bot", true, true, "v2") {
		t.Fatal("configuration change must restart the streak")
	}
	observe("bot", true, true, "v2")
	s.Clear()
	if observe("bot", true, true, "v2") {
		t.Fatal("clearing session must restart the streak")
	}
}
