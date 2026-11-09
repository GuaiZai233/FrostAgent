package onebot

import (
	"encoding/json"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/parity"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

// buildResponseContext creates per-turn, user-segment guidance for the model's
// reply-or-silence decision. It deliberately avoids the system prompt so
// dynamic trigger metadata does not invalidate the stable system-prompt cache.
func buildResponseContext(
	event model.OneBotEvent,
	wakeSignals GroupWakeSignals,
	quotedAlias bool,
) string {
	triggers := make([]string, 0, 4)
	if event.MessageType == "private" {
		triggers = append(triggers, "private_message")
	} else {
		if wakeSignals.Poke {
			triggers = append(triggers, "poke")
		}
		if wakeSignals.AtBot {
			triggers = append(triggers, "at")
		}
		if wakeSignals.Alias {
			triggers = append(triggers, "alias")
		}
		if wakeSignals.Proactive {
			triggers = append(triggers, "proactive")
		}
		if quotedAlias {
			triggers = append(triggers, "quoted_alias")
		}
	}

	guidance := parity.ReplyDecisionGuidance
	if wakeSignals.Alias {
		guidance = parity.AliasWakeGuidance + " " + guidance
	}
	context := map[string]interface{}{
		"mode":              event.MessageType,
		"triggers":          triggers,
		"silence_available": true,
		"guidance":          guidance + parity.CurrentTimeGuidance(time.Now()),
	}
	encoded, _ := json.Marshal(context)
	return string(encoded)
}
