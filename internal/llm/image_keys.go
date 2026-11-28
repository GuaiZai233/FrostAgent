package llm

import "regexp"

// imagePlaceholderPattern matches the text a conversation shows for an image:
// "[图片]" from users and "[image ...]" from the bot. Console previews pair one
// preview key with each match, in order.
var imagePlaceholderPattern = regexp.MustCompile(`\[图片\]|\[image(?: [^\]]*)?\]`)

// CountImagePlaceholders reports how many image placeholders text contains.
func CountImagePlaceholders(text string) int {
	return len(imagePlaceholderPattern.FindAllStringIndex(text, -1))
}

// alignImageKeys returns exactly one key per placeholder in text, padding with
// "" (no preview) or truncating so later concatenation stays aligned.
func alignImageKeys(text string, keys []string) []string {
	count := CountImagePlaceholders(text)
	if count == 0 {
		return nil
	}
	aligned := make([]string, count)
	copy(aligned, keys)
	return aligned
}

func anyImageKey(keys []string) bool {
	for _, key := range keys {
		if key != "" {
			return true
		}
	}
	return false
}

// SetHistoryImageKeys attaches console preview keys to the history message with
// the given sequence number. Keys never reach a model.
func (s *SessionContext) SetHistoryImageKeys(seq uint64, keys []string) {
	if s == nil || seq == 0 || !anyImageKey(keys) {
		return
	}
	s.mu.Lock()
	defer s.unlockCheckpoint()
	s.setHistoryImageKeysLocked(seq, keys)
}

func (s *SessionContext) setHistoryImageKeysLocked(seq uint64, keys []string) {
	for i, id := range s.historySeq {
		if id == seq && i < len(s.History) {
			s.History[i].ImageKeys = append([]string(nil), keys...)
			return
		}
	}
}
