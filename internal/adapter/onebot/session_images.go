package onebot

import (
	"context"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

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
		// Bounded and de-duplicated: a full queue simply skips the preview.
		store.FetchSessionImage(key, func(ctx context.Context) ([]byte, error) {
			return content.FetchSegmentImage(ctx, seg)
		})
	}
	if !found {
		return nil
	}
	return keys
}
