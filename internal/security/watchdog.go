package security

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type WatchdogStage string
type WatchdogSource string
type WatchdogAction string

type WatchdogDecision struct {
	Action WatchdogAction `json:"action"`
	Reason string         `json:"reason,omitempty"`
	Event  AuditEvent     `json:"-"`
}

const (
	StageIngress      WatchdogStage = "INGRESS"
	StageToolArgument WatchdogStage = "TOOL_ARGUMENT"
	StageToolResult   WatchdogStage = "TOOL_RESULT"
	StageModelOutput  WatchdogStage = "MODEL_OUTPUT"

	SourceUserDirect   WatchdogSource = "USER_DIRECT"
	SourceUserQuote    WatchdogSource = "USER_QUOTE_REPLY_CONTEXT"
	SourceGroupContext WatchdogSource = "GROUP_CONTEXT"
	SourceToolArgument WatchdogSource = "TOOL_ARGUMENT"
	SourceToolResult   WatchdogSource = "TOOL_RESULT"
	SourceModelOutput  WatchdogSource = "MODEL_OUTPUT"
	SourceVisionResult WatchdogSource = "VISION_RESULT"
	SourcePlatformMeta WatchdogSource = "PLATFORM_METADATA"

	WatchdogPass   WatchdogAction = "PASS"
	WatchdogBlock  WatchdogAction = "BLOCK"
	WatchdogStrike WatchdogAction = "STRIKE"
	WatchdogLock   WatchdogAction = "LOCK"
)

type AuditEvent struct {
	ID        string         `json:"id"`
	At        time.Time      `json:"at"`
	Principal Principal      `json:"principal"`
	Instance  string         `json:"instance,omitempty"`
	Session   string         `json:"session,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Stage     WatchdogStage  `json:"stage"`
	Source    WatchdogSource `json:"source"`
	Action    WatchdogAction `json:"action"`
	Reason    string         `json:"reason,omitempty"`
	Hash      string         `json:"content_hash"`
	Preview   string         `json:"preview,omitempty"`
	Encoded   bool           `json:"encoded,omitempty"`
}

type AuditStore struct {
	path  string
	limit int
	mu    sync.Mutex
}

func NewAuditStore(path string, limit int) *AuditStore {
	if limit <= 0 {
		limit = 1000
	}
	return &AuditStore{path: path, limit: limit}
}

func (s *AuditStore) Append(event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = ContentHash(event.At.String() + event.Hash)[:16]
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := ensureParent(s.path); err != nil {
		return err
	}
	lock, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer lock.release()
	existing, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(existing)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	lines = append(lines, string(data))
	if len(lines) > s.limit {
		lines = lines[len(lines)-s.limit:]
	}
	content := strings.Join(lines, "\n") + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".security-audit-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return atomicReplaceFile(tmpName, s.path)
}

func (s *AuditStore) List(limit int) ([]AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := osReadFile(s.path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(data), "\n")
	result := make([]AuditEvent, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event AuditEvent
		if json.Unmarshal([]byte(line), &event) == nil {
			result = append(result, event)
		}
	}
	if limit <= 0 || limit > len(result) {
		limit = len(result)
	}
	if limit == 0 {
		return []AuditEvent{}, nil
	}
	return result[len(result)-limit:], nil
}

// Watchdog evaluates content and never directly locks on model/tool output.
// Locking is reserved for active, repeatable, attributable user behavior.
type Watchdog struct {
	access       *AccessStore
	audit        *AuditStore
	strikeWindow time.Duration
	lockAfter    int
}

func NewWatchdog(access *AccessStore, audit *AuditStore) *Watchdog {
	return &Watchdog{access: access, audit: audit, strikeWindow: 15 * time.Minute, lockAfter: 3}
}

const (
	MaxInspectionSize = 256 * 1024 // 256 KiB
)

func (w *Watchdog) Evaluate(p Principal, stage WatchdogStage, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	if len(content) > MaxInspectionSize {
		meta.At = time.Now().UTC()
		meta.Principal = p
		meta.Stage = stage
		meta.Source = source
		meta.Action = WatchdogBlock
		meta.Reason = "input exceeds maximum inspection limit"
		meta.Hash = ContentHash(content)
		meta.Preview = safePreview(content)
		if w.audit != nil {
			_ = w.audit.Append(meta)
		}
		return WatchdogDecision{Action: WatchdogBlock, Reason: meta.Reason, Event: meta}
	}
	rawContent := content
	content, _ = normalizeBounded(content)
	rawMatches := dangerousContent(rawContent)
	normMatches := dangerousContent(content)

	rawHash := ContentHash(rawContent)
	normHash := ContentHash(content)

	var lastBlockedHash string
	if w.access != nil {
		lastBlockedHash = w.access.LastBlockedHash(p, time.Now().UTC().Add(-w.strikeWindow))
	}

	isEvasion := (!rawMatches && normMatches) || (lastBlockedHash != "" && normHash == lastBlockedHash && rawHash != normHash)

	action := WatchdogPass
	reason := ""
	if normMatches {
		reason = "content matched deterministic high-risk rule"
		switch source {
		case SourceUserDirect:
			strikes, locked, err := w.access.RecordBlockedSubmission(p, normHash, isEvasion, time.Now().UTC(), w.strikeWindow, w.lockAfter)
			if err == nil && locked {
				action = WatchdogLock
				reason = "repeated active attempts to evade watchdog blocks"
			} else if err == nil && strikes > 0 {
				action = WatchdogStrike
			} else {
				action = WatchdogBlock
			}
		default:
			action = WatchdogBlock
		}
	}
	meta.At = time.Now().UTC()
	meta.Principal = p
	meta.Stage = stage
	meta.Source = source
	meta.Action = action
	meta.Reason = reason
	meta.Hash = normHash
	meta.Encoded = isEvasion
	meta.Preview = safePreview(content)
	if w.audit != nil && action != WatchdogPass {
		_ = w.audit.Append(meta)
	}
	return WatchdogDecision{Action: action, Reason: reason, Event: meta}
}

func (w *Watchdog) IsLocked(p Principal) bool {
	if w == nil || w.access == nil {
		return false
	}
	locked, _, err := w.access.IsLocked(p)
	return err == nil && locked
}

var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all|any|the)\s+previous`),
	regexp.MustCompile(`(?i)disable\s+(the\s+)?safety|bypass\s+(the\s+)?(watchdog|policy|lock)`),
	regexp.MustCompile(`(?i)(rm\s+-rf\s+/|del\s+/f\s+/s\s+/q|format\s+[a-z]:)`),
	regexp.MustCompile(`(?i)(curl|wget)\s+[^\n|]{0,512}\|\s*(sh|bash|powershell)`),
	regexp.MustCompile(`(?i)exfiltrat(e|ion)|steal\s+(api|access|session)\s*keys`),
}

func dangerousContent(content string) bool {
	for _, pattern := range dangerousPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}

// tolerantPercentUnescape scans s and decodes any valid %[0-9a-fA-F]{2} sequence
// into its single byte value, while preserving malformed escapes (e.g. %ZZ, dangling %)
// and '+' verbatim.
func tolerantPercentUnescape(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 3
			changed = true
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	if !changed {
		return s, false
	}
	return b.String(), true
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func stripZeroWidthAndControl(s string) (string, bool) {
	stripped := strings.Map(func(r rune) rune {
		switch r {
		case rune(0x200b), rune(0x200c), rune(0x200d), rune(0x200e), rune(0x200f), rune(0x2060), rune(0xfeff):
			return -1
		default:
			if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
				return -1
			}
			return r
		}
	}, s)
	return stripped, stripped != s
}

func normalizeBounded(content string) (string, bool) {
	if len(content) > MaxInspectionSize {
		content = content[:MaxInspectionSize]
	}
	content, stripped := stripZeroWidthAndControl(content)
	modified := stripped

	for range 3 {
		layerChanged := false

		// 1. Use tolerant percent unescape so mixed valid/malformed escapes (%xx with %ZZ)
		// are decoded without error, while '+' (e.g. in C++ or A+B) is preserved literally.
		if decoded, ok := tolerantPercentUnescape(content); ok && decoded != content && len(decoded) <= MaxInspectionSize {
			content = decoded
			modified = true
			layerChanged = true
			if s, st := stripZeroWidthAndControl(content); st {
				content = s
			}
		}

		// 2. Base64 unescape
		trimmed := strings.TrimSpace(content)
		decodedBytes, err := base64.StdEncoding.DecodeString(trimmed)
		if err == nil && len(decodedBytes) > 0 && len(decodedBytes) <= MaxInspectionSize {
			decoded := string(decodedBytes)
			if decoded != content && isValidPrintableText(decoded) {
				content = decoded
				modified = true
				layerChanged = true
				if s, st := stripZeroWidthAndControl(content); st {
					content = s
				}
			}
		}

		if !layerChanged {
			break
		}
	}
	return content, modified
}

func isValidPrintableText(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func ensureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}

func osReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

var (
	authHeaderRegex     = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer\s+|basic\s+|token\s+)?)[^\r\n,;]+`)
	bearerRegex         = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9_\-\.~+/=]{6,}`)
	prefixedKeyRegex    = regexp.MustCompile(`\b(?:github_pat_[A-Za-z0-9_]{20,}|(?:sk|pk|ghp|gho|ghu|ghs|ghr|glpat|xox[baprs])[_-][A-Za-z0-9_\-]{8,})\b`)
	urlQuerySecretRegex = regexp.MustCompile(`(?i)([?&](?:access_token|api_key|apikey|token|secret|key|password)=)[^&\s]+`)
	jsonSecretRegex     = regexp.MustCompile(`(?i)("(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|private[_-]?key)"\s*:\s*)"[^"]+"`)
	kvSecretRegex       = regexp.MustCompile(`(?i)\b((?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|private[_-]?key)\s*[:=]\s*)["']?[^\s"',;&]+["']?`)
	basicAuthRegex      = regexp.MustCompile(`(https?://)([^:\s]+):([^@\s]+)@`)
)

func redactSecrets(s string) string {
	s = authHeaderRegex.ReplaceAllString(s, "${1}[REDACTED]")
	s = bearerRegex.ReplaceAllString(s, "${1}[REDACTED]")
	s = prefixedKeyRegex.ReplaceAllString(s, "[REDACTED]")
	s = urlQuerySecretRegex.ReplaceAllString(s, "${1}[REDACTED]")
	s = jsonSecretRegex.ReplaceAllString(s, `${1}"[REDACTED]"`)
	s = kvSecretRegex.ReplaceAllString(s, "${1}[REDACTED]")
	s = basicAuthRegex.ReplaceAllString(s, "${1}[REDACTED]:[REDACTED]@")
	return s
}

func safePreview(content string) string {
	content = redactSecrets(content)
	content = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, content)
	if len(content) > 160 {
		content = content[:160]
	}
	return content
}
