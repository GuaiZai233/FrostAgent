package onebot

import (
	"encoding/json"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestGroupProbabilityOverrideRouting(t *testing.T) {
	// Small synthetic IDs are unrelated to real users or groups.
	event := model.OneBotEvent{PostType: "message", MessageType: "group", GroupID: 42, UserID: 7, SelfID: 11, Message: json.RawMessage(`"synthetic conversation"`)}
	for _, tc := range []struct {
		overrides string
		want      bool
	}{
		{`{"qq:42":0.25}`, true}, {`{"onebot:42":0.25}`, true}, {`{"42":0.25}`, true},
		{`{"telegram:42":0.25}`, false}, {`{"qq:42":0}`, false}, {`{"qq:43":1}`, false},
	} {
		scope := newTestScopeWithEnv(t, map[string]string{
			proactive.EnvProbability: "0.05", proactive.EnvGroupProbabilities: tc.overrides,
		})
		signals := DetectGroupWakeSignalsWithRNG(event, func() float64 { return 0.2 }, scope)
		if signals.Proactive != tc.want || signals.Any() != tc.want {
			t.Fatalf("%s: got %+v, want proactive=%v", tc.overrides, signals, tc.want)
		}
	}
}
