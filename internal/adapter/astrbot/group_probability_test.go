package astrbot

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestGroupProbabilityOverrideRouting(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		proactive.EnvProbability:        "0.05",
		proactive.EnvGroupProbabilities: `{"qq:synthetic-group":0.25,"telegram:synthetic-group":0.38,"discord:synthetic-group":0}`,
	})
	for _, tc := range []struct {
		platform string
		roll     float64
		want     bool
	}{
		{"onebot", 0.2, true}, {"aiocqhttp", 0.2, true}, {"qq", 0.3, false},
		{"telegram", 0.3, true}, {"telegram", 0.38, false}, {"discord", 0, false}, {"", 0.2, false},
	} {
		event := Event{Type: "event", EventType: "message", MessageType: "group", GroupID: "synthetic-group", UserID: "synthetic-user", Platform: tc.platform, Content: "synthetic conversation"}
		if got := shouldReplyWithRNG(&event, func() float64 { return tc.roll }, scope); got != tc.want || isProactiveReply(&event) != tc.want {
			t.Fatalf("%s/%v: reply=%v metadata=%v, want %v", tc.platform, tc.roll, got, event.Metadata, tc.want)
		}
	}
	event := Event{Type: "event", EventType: "message", MessageType: "group", GroupID: "synthetic-group", UserID: "synthetic-user", Platform: "discord", IsWake: true}
	if !shouldReplyWithRNG(&event, func() float64 { t.Fatal("explicit wake must not roll"); return 0 }, scope) || isProactiveReply(&event) {
		t.Fatal("zero override must not suppress an explicit wake")
	}
}
