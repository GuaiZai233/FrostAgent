package security

import (
	"FrostAgent/internal/core"
	"context"
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
	Action         WatchdogAction        `json:"action"`
	Reason         string                `json:"reason,omitempty"`
	Classification *ClassificationResult `json:"classification,omitempty"`
	Event          AuditEvent            `json:"-"`
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
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	Principal  Principal      `json:"principal"`
	Instance   string         `json:"instance,omitempty"`
	Session    string         `json:"session,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Stage      WatchdogStage  `json:"stage"`
	Source     WatchdogSource `json:"source"`
	Action     WatchdogAction `json:"action"`
	Reason     string         `json:"reason,omitempty"`
	Hash       string         `json:"content_hash"`
	Preview    string         `json:"preview,omitempty"`
	Encoded    bool           `json:"encoded,omitempty"`
	Category   RiskCategory   `json:"category,omitempty"`
	RiskLevel  RiskLevel      `json:"risk_level,omitempty"`
	Intent     ActorIntent    `json:"intent,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
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
	mu                  sync.RWMutex
	access              *AccessStore
	audit               *AuditStore
	classifier          Classifier
	instanceClassifiers map[string]Classifier
	strikeWindow        time.Duration
	lockAfter           int
}

func NewWatchdog(access *AccessStore, audit *AuditStore) *Watchdog {
	return &Watchdog{
		access:              access,
		audit:               audit,
		classifier:          NewHybridClassifier(nil, NewCalibratedClassifier()),
		instanceClassifiers: make(map[string]Classifier),
		strikeWindow:        15 * time.Minute,
		lockAfter:           3,
	}
}

func NewWatchdogWithProvider(access *AccessStore, audit *AuditStore, provider core.LLMProvider, model string) *Watchdog {
	wd := NewWatchdog(access, audit)
	if provider != nil {
		wd.SetLLMProvider(provider, model)
	}
	return wd
}

func (w *Watchdog) SetClassifier(classifier Classifier) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if classifier != nil {
		w.classifier = classifier
	}
}

func (w *Watchdog) SetLLMProvider(provider core.LLMProvider, model string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if provider == nil {
		w.classifier = NewCalibratedClassifier()
		return
	}
	if model == "" {
		model = "security-gateway"
	}
	llmCls := NewLLMClassifier(provider, model, 5*time.Second)
	w.classifier = NewHybridClassifier(llmCls, NewCalibratedClassifier())
}

func (w *Watchdog) SetInstanceProvider(instanceID string, provider core.LLMProvider, model string) {
	if w == nil || instanceID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.instanceClassifiers == nil {
		w.instanceClassifiers = make(map[string]Classifier)
	}
	if provider == nil {
		delete(w.instanceClassifiers, instanceID)
		return
	}
	if model == "" {
		model = "security-gateway"
	}
	llmCls := NewLLMClassifier(provider, model, 5*time.Second)
	w.instanceClassifiers[instanceID] = NewHybridClassifier(llmCls, NewCalibratedClassifier())
}

func (w *Watchdog) SetInstanceClassifier(instanceID string, classifier Classifier) {
	if w == nil || instanceID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.instanceClassifiers == nil {
		w.instanceClassifiers = make(map[string]Classifier)
	}
	if classifier == nil {
		delete(w.instanceClassifiers, instanceID)
		return
	}
	w.instanceClassifiers[instanceID] = classifier
}

func (w *Watchdog) RemoveInstanceProvider(instanceID string) {
	if w == nil || instanceID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.instanceClassifiers != nil {
		delete(w.instanceClassifiers, instanceID)
	}
}

func (w *Watchdog) Classifier() Classifier {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.classifier
}

func (w *Watchdog) ClassifierForInstance(instanceID string) Classifier {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if instanceID != "" && w.instanceClassifiers != nil {
		if cls, ok := w.instanceClassifiers[instanceID]; ok {
			return cls
		}
	}
	return w.classifier
}

const (
	MaxInspectionSize = 256 * 1024 // 256 KiB
)

func (w *Watchdog) Evaluate(p Principal, stage WatchdogStage, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	return w.EvaluateWithContext(context.Background(), p, stage, source, content, meta)
}

func (w *Watchdog) EvaluateWithContext(ctx context.Context, p Principal, stage WatchdogStage, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	if ctx == nil {
		ctx = context.Background()
	}
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
		return WatchdogDecision{
			Action: WatchdogBlock,
			Reason: meta.Reason,
			Event:  meta,
		}
	}

	rawContent := content
	normalized, _ := normalizeBounded(content)

	rawHash := ContentHash(rawContent)
	normHash := ContentHash(normalized)

	var lastBlockedHash string
	hasPriorBlock := false
	if w.access != nil {
		lastBlockedHash = w.access.LastBlockedHash(p, time.Now().UTC().Add(-w.strikeWindow))
		hasPriorBlock = lastBlockedHash != ""
	}

	var classifier Classifier
	if w != nil {
		w.mu.RLock()
		if meta.Instance != "" && w.instanceClassifiers != nil {
			classifier = w.instanceClassifiers[meta.Instance]
		}
		if classifier == nil {
			classifier = w.classifier
		}
		w.mu.RUnlock()
	}
	if classifier == nil {
		classifier = NewCalibratedClassifier()
	}

	normInput := ClassificationInput{
		Content:         rawContent,
		Normalized:      normalized,
		Stage:           stage,
		Origin:          source,
		Principal:       p,
		LastBlockedHash: lastBlockedHash,
		HasPriorBlock:   hasPriorBlock,
	}

	classifierErr := false
	normClassification, err := classifier.Classify(ctx, normInput)
	if err != nil {
		// Fail-closed on classifier failure: guaranteed block without striking/locking user
		classifierErr = true
		normClassification = ClassificationResult{
			Category:   RiskCategoryPromptInjection,
			RiskLevel:  RiskLevelHigh,
			Intent:     IntentAmbiguous,
			Confidence: 0.90,
			Origin:     source,
			Reason:     "classifier evaluation error; fail-closed block",
		}
	}

	rawInput := normInput
	rawInput.Normalized = rawContent
	rawClassification, errRaw := classifier.Classify(ctx, rawInput)
	if errRaw != nil {
		rawClassification = normClassification
	}

	rawMatches := rawClassification.IsRisky()
	normMatches := normClassification.IsRisky()
	if classifierErr {
		normMatches = true
	}

	isEvasion := (!rawMatches && normMatches) || (hasPriorBlock && normHash == lastBlockedHash && rawHash != normHash)

	action := WatchdogPass
	reason := ""

	isRisky := normMatches || rawMatches || classifierErr
	classification := normClassification
	if !normMatches && rawMatches {
		classification = rawClassification
	}

	if isRisky {
		reason = classification.Reason
		if reason == "" {
			reason = "content matched security policy violation"
		}
		if classifierErr {
			// Fail-closed block directly without accumulating strikes or locks
			action = WatchdogBlock
			reason = "classifier evaluation error; fail-closed block"
		} else {
			switch source {
			case SourceUserDirect:
				// Only attributable malicious intent with high confidence can accumulate strikes or lock a principal.
				// Benign or ambiguous intent, as well as low-confidence results, strictly block content without penalizing the user.
				if classification.Intent != IntentMalicious || classification.Confidence < 0.70 {
					action = WatchdogBlock
				} else {
					strikes, locked, err := w.access.RecordBlockedSubmission(p, normHash, isEvasion, time.Now().UTC(), w.strikeWindow, w.lockAfter)
					if err == nil && locked {
						action = WatchdogLock
						reason = "repeated active attempts to evade watchdog blocks"
					} else if err == nil && strikes > 0 {
						action = WatchdogStrike
					} else {
						action = WatchdogBlock
					}
				}
			default:
				// Non-direct sources (quotes, group summaries, tool arguments/results, model outputs, vision, platform meta)
				// strictly block content without adding strikes or locking the user.
				action = WatchdogBlock
			}
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
	meta.Category = classification.Category
	meta.RiskLevel = classification.RiskLevel
	meta.Intent = classification.Intent
	meta.Confidence = classification.Confidence

	if w.audit != nil && action != WatchdogPass {
		_ = w.audit.Append(meta)
	}

	return WatchdogDecision{
		Action:         action,
		Reason:         reason,
		Classification: &classification,
		Event:          meta,
	}
}

func (w *Watchdog) IsLocked(p Principal) bool {
	if w == nil || w.access == nil {
		return false
	}
	locked, _, err := w.access.IsLocked(p)
	return err == nil && locked
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
