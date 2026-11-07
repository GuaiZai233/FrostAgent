// Package replyformat interprets the model's ordinary reply delivery markers.
package replyformat

import "strings"

const EnvKey = "ENABLE_MULTI_SEGMENT_REPLY"
const SegmentMark = "<FROSTAGENT_SEGMENT_MARK>"

// Split preserves code spans/fences and single newlines. Disabled segmentation
// only removes bare markers; ordinary text and whitespace remain unchanged.
func Split(text string, enabled bool) []string {
	codeEnds := protectedCodeEnds(text)
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

// Fences take precedence over inline spans. A closing backtick fence must be
// on its own line (up to three leading spaces), at least as wide as the opener,
// with only trailing spaces/tabs. Backticks inside code lines are literal.
func protectedCodeEnds(text string) map[int]int {
	ends := make(map[int]int)
	plainStart, fenceStart, fenceWidth := 0, -1, 0
	for lineStart := 0; lineStart < len(text); {
		lineEnd := len(text)
		if n := strings.IndexByte(text[lineStart:], '\n'); n >= 0 {
			lineEnd = lineStart + n
		}
		line := strings.TrimSuffix(text[lineStart:lineEnd], "\r")
		indent := 0
		for indent < len(line) && indent < 4 && line[indent] == ' ' {
			indent++
		}
		end := indent
		for end < len(line) && line[end] == '`' {
			end++
		}
		width := end - indent
		if indent <= 3 && width >= 3 {
			if fenceStart < 0 {
				// Backticks in the info string instead denote an inline span.
				if !strings.ContainsRune(line[end:], '`') {
					fenceStart, fenceWidth = lineStart, width
				}
			} else if width >= fenceWidth && strings.Trim(line[end:], " \t") == "" {
				indexInlineCode(text, plainStart, fenceStart, ends)
				// Leave the entire line ending outside the protected interval
				// so a following CRLF paragraph separator remains detectable.
				fenceEnd := lineStart + len(line)
				ends[fenceStart] = fenceEnd
				plainStart, fenceStart = fenceEnd, -1
			}
		}
		lineStart = lineEnd + 1
	}
	if fenceStart >= 0 {
		indexInlineCode(text, plainStart, fenceStart, ends)
		ends[fenceStart] = len(text)
	} else {
		indexInlineCode(text, plainStart, len(text), ends)
	}
	return ends
}

// Pre-index equal-width inline delimiters in each non-fenced region. Each byte
// is visited a bounded number of times, including unmatched/adversarial input.
func indexInlineCode(text string, start, end int, ends map[int]int) {
	type tickRun struct{ start, end int }
	var runs []tickRun
	for i := start; i < end; {
		if text[i] != '`' {
			i++
			continue
		}
		begin := i
		for i < end && text[i] == '`' {
			i++
		}
		runs = append(runs, tickRun{begin, i})
	}
	next := make(map[int]int)
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		width := run.end - run.start
		if close, ok := next[width]; ok {
			ends[run.start] = close
		}
		next[width] = run.end
	}
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
