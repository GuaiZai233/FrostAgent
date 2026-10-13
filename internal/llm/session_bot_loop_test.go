package llm

import (
	"testing"
	"time"
)

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

func TestSessionBotLoopObservationsSettleInIngressOrder(t *testing.T) {
	s := &SessionContext{}
	first := s.BeginBotLoop("bot-a", true, "revision")
	human := s.BeginBotLoop("human-a", false, "revision")
	settled := make(chan bool, 1)
	go func() { settled <- human.Finish(false, 1) }()
	select {
	case <-settled:
		t.Fatal("later human event settled before prior bot event")
	case <-time.After(20 * time.Millisecond):
	}
	if first.Finish(true, 1) {
		t.Fatal("first bot turn exceeded limit")
	}
	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Fatal("human interruption did not settle")
	}
	if s.BeginBotLoop("bot-a", true, "revision").Finish(true, 1) {
		t.Fatal("bot turn after human interruption was counted against prior streak")
	}
	if !s.BeginBotLoop("bot-a", true, "revision").Finish(true, 1) {
		t.Fatal("next consecutive bot turn must exceed limit")
	}
}

func TestSessionBotLoopAbortedObservationReleasesNext(t *testing.T) {
	s := &SessionContext{}
	aborted := s.BeginBotLoop("bot-a", true, "revision")
	next := s.BeginBotLoop("bot-a", true, "revision")
	aborted.Finish(false, 1)
	if next.Finish(true, 1) {
		t.Fatal("aborted request must not consume a turn")
	}
}
