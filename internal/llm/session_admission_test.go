package llm

import "testing"

func TestBotLoopAdmissionDoesNotCountCooldownLoser(t *testing.T) {
	session := &SessionContext{}
	first := session.BeginBotLoop("synthetic-bot", true, "config")
	if exceeded, admitted := first.FinishWithAdmission(true, 2, func(bool) bool { return true }); exceeded || !admitted {
		t.Fatal("first eligible turn rejected")
	}
	rejected := session.BeginBotLoop("synthetic-bot", true, "config")
	if _, admitted := rejected.FinishWithAdmission(true, 2, func(bool) bool { return false }); admitted {
		t.Fatal("cooldown loser admitted")
	}
	next := session.BeginBotLoop("synthetic-bot", true, "config")
	if exceeded, admitted := next.FinishWithAdmission(true, 2, func(bool) bool { return true }); exceeded || !admitted {
		t.Fatal("cooldown loser consumed a bot-loop turn")
	}
	if !session.BeginBotLoop("synthetic-bot", true, "config").Finish(true, 2) {
		t.Fatal("accepted turns no longer enforce bot-loop limit")
	}
}
func TestBotLoopRejectedAdmissionDoesNotResetStreak(t *testing.T) {
	session := &SessionContext{}
	session.BeginBotLoop("synthetic-bot", true, "config").Finish(true, 1)
	session.BeginBotLoop("synthetic-human", false, "config", true).FinishWithAdmission(true, 1, func(bool) bool { return false })
	if !session.BeginBotLoop("synthetic-bot", true, "config").Finish(true, 1) {
		t.Fatal("cooldown loser reset accepted bot-loop streak")
	}
}
func TestBotLoopAdmissionWithoutObservation(t *testing.T) {
	var observation *BotLoopObservation
	called := false
	_, admitted := observation.FinishWithAdmission(true, 1, func(exceeded bool) bool {
		called = true
		if exceeded {
			t.Fatal("nil observation exceeded")
		}
		return false
	})
	if !called || admitted {
		t.Fatal("missing cooldown admission without bot-loop tracking")
	}
}
