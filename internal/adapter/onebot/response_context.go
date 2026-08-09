package onebot

import (
	"FrostAgent/internal/model"
	"encoding/json"
)

// buildResponseContext creates per-turn, user-segment guidance for the model's
// reply-or-silence decision. It deliberately avoids the system prompt so
// dynamic trigger metadata does not invalidate the stable system-prompt cache.
func buildResponseContext(
	event model.OneBotEvent,
	wakeSignals GroupWakeSignals,
	quotedAlias bool,
) string {
	triggers := make([]string, 0, 3)
	if event.MessageType == "private" {
		triggers = append(triggers, "private_message")
	} else {
		if wakeSignals.AtBot {
			triggers = append(triggers, "at")
		}
		if wakeSignals.Alias {
			triggers = append(triggers, "alias")
		}
		if quotedAlias {
			triggers = append(triggers, "quoted_alias")
		}
	}

	context := map[string]interface{}{
		"mode":              event.MessageType,
		"triggers":          triggers,
		"silence_available": true,
		"guidance":          "请结合用户消息与已有上下文自行决定是否回复；确实无需回应时，单独调用 stay_silent。用户明确要求回应时应正常回答。",
	}
	encoded, _ := json.Marshal(context)
	return string(encoded)
}
