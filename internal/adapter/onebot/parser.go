package onebot

import (
	"FrostAgent/internal/model"
	"encoding/json"
	"strconv"
)

type MessageSegment struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func IsMentionedBot(event model.OneBotEvent) bool {
	if event.MessageType != "group" {
		return false
	}

	var segments []MessageSegment
	if err := json.Unmarshal(event.Message, &segments); err != nil {
		return false
	}

	selfIDStr := strconv.FormatInt(event.SelfID, 10)

	/*
		In OneBot V11 protocol, underlying implementation of `at` component is different,
		so use type assertion here.
	*/
	for _, seg := range segments {
		if seg.Type == "at" {
			qqVal := seg.Data["qq"]
			var atQQ string

			switch v := qqVal.(type) {
			case string:
				atQQ = v
			case float64:
				atQQ = strconv.FormatFloat(v, 'f', -1, 64)
			}

			if atQQ == selfIDStr {
				return true
			}
		}
	}

	return false
}
