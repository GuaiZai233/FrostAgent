package onebot

import (
	"context"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

const sessionImageFetchTimeout = 20 * time.Second

// retainEventImages returns one cache key per image placeholder in the event
// (in message order, "" when the image has no trusted source) and fetches the
// images in the background so the console can preview them on hover.
func retainEventImages(event model.OneBotEvent, engine *llm.Engine) []string {
	if engine == nil {
		return nil
	}
	store := runtimescope.First([]*runtimescope.Scope{engine.Scope}).Log()
	var keys []string
	found := false
	for _, seg := range ParseMessageSegments(event.Message) {
		if seg.Type != "image" && seg.Type != "mface" {
			continue
		}
		key := content.SegmentImageKey(seg)
		keys = append(keys, key)
		if key == "" {
			continue
		}
		found = true
		if store.HasSessionImage(key) {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), sessionImageFetchTimeout)
			defer cancel()
			if data, err := content.FetchSegmentImage(ctx, seg); err == nil {
				store.PutSessionImage(key, data)
			}
		}()
	}
	if !found {
		return nil
	}
	return keys
}
