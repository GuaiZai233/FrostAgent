package onebot

import (
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/poke"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func noEnv(string) string { return "" }

func TestBannedRejectCooldownDefaultsAndOverrides(t *testing.T) {
	if got := bannedRejectCooldown(noEnv); got != 180*time.Second {
		t.Fatalf("default cooldown = %s, want 180s", got)
	}
	env := func(string) string { return "60" }
	if got := bannedRejectCooldown(env); got != 60*time.Second {
		t.Fatalf("override cooldown = %s, want 60s", got)
	}
	for _, bad := range []string{"abc", "-1", "86401"} {
		invalid := func(string) string { return bad }
		if got := bannedRejectCooldown(invalid); got != 180*time.Second {
			t.Fatalf("invalid %q cooldown = %s, want default 180s", bad, got)
		}
	}
}

func TestAllowBannedRejectReplyPokeAndAliasStaySilent(t *testing.T) {
	var gate poke.Gate
	now := time.Unix(1_000_000, 0)
	group := model.OneBotEvent{MessageType: "group"}
	poked := model.OneBotEvent{MessageType: "group", PokeID: "poke_1"}

	if allowBannedRejectReply(poked, GroupWakeSignals{Poke: true}, &gate, noEnv, "k", now) {
		t.Fatal("poke must never receive a banned rejection")
	}
	if allowBannedRejectReply(group, GroupWakeSignals{Alias: true}, &gate, noEnv, "k", now) {
		t.Fatal("alias-only mention must never receive a banned rejection")
	}
	if allowBannedRejectReply(model.OneBotEvent{MessageType: "group", PokeID: "p"}, GroupWakeSignals{AtBot: true, Poke: true}, &gate, noEnv, "k", now) {
		t.Fatal("poke takes precedence over at")
	}
}

func TestAllowBannedRejectReplyAtMentionIsRateLimited(t *testing.T) {
	var gate poke.Gate
	now := time.Unix(1_000_000, 0)
	group := model.OneBotEvent{MessageType: "group"}
	at := GroupWakeSignals{AtBot: true}

	if !allowBannedRejectReply(group, at, &gate, noEnv, "user:group", now) {
		t.Fatal("first @ rejection should be sent")
	}
	if allowBannedRejectReply(group, at, &gate, noEnv, "user:group", now.Add(179*time.Second)) {
		t.Fatal("@ rejection inside 180s cooldown should be suppressed")
	}
	if !allowBannedRejectReply(group, at, &gate, noEnv, "user:group", now.Add(180*time.Second)) {
		t.Fatal("@ rejection after 180s cooldown should be sent again")
	}
	if !allowBannedRejectReply(group, at, &gate, noEnv, "user:other-group", now) {
		t.Fatal("cooldown must be scoped per conversation")
	}
}

func TestAllowBannedRejectReplyPrivateKeepsExistingBehavior(t *testing.T) {
	var gate poke.Gate
	now := time.Unix(1_000_000, 0)
	private := model.OneBotEvent{MessageType: "private"}
	poked := model.OneBotEvent{MessageType: "private", PokeID: "poke_1"}

	for i := 0; i < 2; i++ {
		if !allowBannedRejectReply(private, GroupWakeSignals{}, &gate, noEnv, "user:private", now) {
			t.Fatalf("private plain rejection %d should still be sent", i)
		}
	}
	if allowBannedRejectReply(poked, GroupWakeSignals{Poke: true}, &gate, noEnv, "user:private", now) {
		t.Fatal("private poke must stay silent")
	}
}

func TestIsBannedRejection(t *testing.T) {
	p, err := security.NewPrincipal("onebot", "1001")
	if err != nil {
		t.Fatal(err)
	}
	var nilController *security.Controller

	if !isBannedRejection(nilController, p, security.WatchdogDecision{Action: security.WatchdogBlock, Reason: security.ErrLocked.Error()}) {
		t.Fatal("ErrLocked reason must count as banned")
	}
	if !isBannedRejection(nilController, p, security.WatchdogDecision{Action: security.WatchdogLock}) {
		t.Fatal("WatchdogLock action must count as banned")
	}
	if isBannedRejection(nilController, p, security.WatchdogDecision{Action: security.WatchdogBlock, Reason: "content"}) {
		t.Fatal("ordinary content block must not count as banned")
	}
}
