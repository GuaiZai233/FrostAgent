// Package replyformat interprets the model's ordinary reply delivery markers.
package replyformat

import "strings"

const EnvKey = "ENABLE_MULTI_SEGMENT_REPLY"
const SegmentMark = "<FROSTAGENT_SEGMENT_MARK>"

// Split preserves code spans/fences and single newlines. Disabled segmentation
// only removes bare markers; ordinary text and whitespace remain unchanged.
func Split(text string, enabled bool) []string {
	// Index matching backtick runs once, so unmatched delimiters cannot hide
	// bare markers and adversarial input cannot cause quadratic rescanning.
	type tickRun struct{ start, end int }
	var runs []tickRun
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(text) && text[i] == '`' {
			i++
		}
		runs = append(runs, tickRun{start, i})
	}
	next := map[int]int{}
	codeEnds := map[int]int{}
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		width := run.end - run.start
		if end, ok := next[width]; ok {
			codeEnds[run.start] = end
		}
		next[width] = run.end
	}
	var parts []string
	var current strings.Builder
	split := false
	flush := func() {
		if part := strings.TrimSpace(current.String()); part != "" {
			parts = append(parts, part)
		}
		current.Reset()
	}
	for i := 0; i < len(text); {
		if end, ok := codeEnds[i]; ok {
			current.WriteString(text[i:end])
			i = end
			continue
		}
		if strings.HasPrefix(text[i:], SegmentMark) {
			i += len(SegmentMark)
			if enabled {
				split = true
				flush()
			}
			continue
		}
		n := 0
		if enabled {
			if strings.HasPrefix(text[i:], "\r\n\r\n") {
				n = 4
			} else if strings.HasPrefix(text[i:], "\n\n") {
				n = 2
			}
		}
		if n > 0 {
			split = true
			flush()
			i += n
			continue
		}
		current.WriteByte(text[i])
		i++
	}
	if !split {
		if strings.TrimSpace(current.String()) == "" {
			return nil
		}
		return []string{current.String()}
	}
	flush()
	return parts
}

// WithReceipt attaches backend-generated billing text only to the last segment.
// Receipts are never parsed as model output or split on their own blank lines.
func WithReceipt(parts []string, receipt string) []string {
	result := append([]string(nil), parts...)
	if receipt == "" {
		return result
	}
	if len(result) == 0 {
		return []string{receipt}
	}
	result[len(result)-1] += "\n\n" + receipt
	return result
}
