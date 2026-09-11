package security

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type RiskCategory string

const (
	RiskCategoryNone               RiskCategory = "none"
	RiskCategoryPromptInjection    RiskCategory = "prompt_injection"
	RiskCategoryMaliciousExecution RiskCategory = "malicious_execution"
	RiskCategoryExfiltration       RiskCategory = "data_exfiltration"
	RiskCategoryPlatformPolicy     RiskCategory = "platform_policy"
	RiskCategoryTencentCompliance  RiskCategory = "tencent_compliance"
	RiskCategoryPolitical          RiskCategory = "political_sensitive"
	RiskCategoryViolence           RiskCategory = "violence_terrorism"
	RiskCategoryVulgarity          RiskCategory = "pornography_vulgarity"
	RiskCategoryFraud              RiskCategory = "fraud_gambling"
)

func (c RiskCategory) IsValid() bool {
	switch c {
	case RiskCategoryNone,
		RiskCategoryPromptInjection,
		RiskCategoryMaliciousExecution,
		RiskCategoryExfiltration,
		RiskCategoryPlatformPolicy,
		RiskCategoryTencentCompliance,
		RiskCategoryPolitical,
		RiskCategoryViolence,
		RiskCategoryVulgarity,
		RiskCategoryFraud:
		return true
	default:
		return false
	}
}

// NormalizeRiskCategory canonicalizes category strings from LLM or external sources.
func NormalizeRiskCategory(s string) (RiskCategory, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "none", "":
		return RiskCategoryNone, true
	case "prompt_injection", "promptinjection", "jailbreak":
		return RiskCategoryPromptInjection, true
	case "malicious_execution", "maliciousexecution", "execution", "command_execution":
		return RiskCategoryMaliciousExecution, true
	case "data_exfiltration", "dataexfiltration", "exfiltration", "leak":
		return RiskCategoryExfiltration, true
	case "platform_policy", "platformpolicy":
		return RiskCategoryPlatformPolicy, true
	case "tencent_compliance", "tencentcompliance":
		return RiskCategoryTencentCompliance, true
	case "political_sensitive", "political":
		return RiskCategoryPolitical, true
	case "violence_terrorism", "violence":
		return RiskCategoryViolence, true
	case "pornography_vulgarity", "vulgarity", "pornography":
		return RiskCategoryVulgarity, true
	case "fraud_gambling", "fraud", "gambling":
		return RiskCategoryFraud, true
	default:
		return RiskCategory(s), false
	}
}

type RiskLevel string

const (
	RiskLevelNone     RiskLevel = "none"
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

func (l RiskLevel) IsValid() bool {
	switch l {
	case RiskLevelNone, RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return true
	default:
		return false
	}
}

// NormalizeRiskLevel canonicalizes risk level strings.
func NormalizeRiskLevel(s string) (RiskLevel, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "none", "":
		return RiskLevelNone, true
	case "low":
		return RiskLevelLow, true
	case "medium", "med":
		return RiskLevelMedium, true
	case "high":
		return RiskLevelHigh, true
	case "critical", "crit":
		return RiskLevelCritical, true
	default:
		return RiskLevel(s), false
	}
}

type ActorIntent string

const (
	IntentBenign    ActorIntent = "benign"
	IntentAmbiguous ActorIntent = "ambiguous"
	IntentMalicious ActorIntent = "malicious"
)

func (i ActorIntent) IsValid() bool {
	switch i {
	case IntentBenign, IntentAmbiguous, IntentMalicious:
		return true
	default:
		return false
	}
}

// NormalizeActorIntent canonicalizes actor intent strings.
func NormalizeActorIntent(s string) (ActorIntent, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "benign", "":
		return IntentBenign, true
	case "ambiguous":
		return IntentAmbiguous, true
	case "malicious":
		return IntentMalicious, true
	default:
		return ActorIntent(s), false
	}
}

// ClassificationInput carries content, provenance, and historical context
// required for calibrated policy evaluation.
type ClassificationInput struct {
	Content         string         `json:"content"`
	Normalized      string         `json:"normalized"`
	Stage           WatchdogStage  `json:"stage"`
	Origin          WatchdogSource `json:"origin"`
	Principal       Principal      `json:"principal"`
	LastBlockedHash string         `json:"last_blocked_hash,omitempty"`
	HasPriorBlock   bool           `json:"has_prior_block,omitempty"`
}

// ClassificationResult represents the structured assessment returned by a Classifier.
type ClassificationResult struct {
	Category   RiskCategory   `json:"category"`
	RiskLevel  RiskLevel      `json:"risk_level"`
	Intent     ActorIntent    `json:"intent"`
	Confidence float64        `json:"confidence"`
	Origin     WatchdogSource `json:"origin"`
	Reason     string         `json:"reason"`
	Details    []string       `json:"details,omitempty"`
}

func (r ClassificationResult) Validate() error {
	if !r.Category.IsValid() {
		return fmt.Errorf("invalid risk category: %q", r.Category)
	}
	if !r.RiskLevel.IsValid() {
		return fmt.Errorf("invalid risk level: %q", r.RiskLevel)
	}
	if !r.Intent.IsValid() {
		return fmt.Errorf("invalid actor intent: %q", r.Intent)
	}
	if math.IsNaN(r.Confidence) || math.IsInf(r.Confidence, 0) || r.Confidence < 0.0 || r.Confidence > 1.0 {
		return fmt.Errorf("confidence out of range [0.0, 1.0]: %v", r.Confidence)
	}
	// Invariant consistency checks
	if r.Category == RiskCategoryNone {
		if r.RiskLevel != RiskLevelNone {
			return fmt.Errorf("category none requires risk_level none, got %q", r.RiskLevel)
		}
		if r.Intent == IntentMalicious {
			return fmt.Errorf("category none cannot have malicious intent")
		}
	} else {
		if r.RiskLevel == RiskLevelNone {
			return fmt.Errorf("non-none category %q cannot have risk_level none", r.Category)
		}
	}
	return nil
}

func (r ClassificationResult) IsRisky() bool {
	if r.Category == RiskCategoryNone || r.RiskLevel == RiskLevelNone {
		return false
	}
	switch r.RiskLevel {
	case RiskLevelCritical:
		return r.Confidence >= 0.3
	case RiskLevelHigh:
		return r.Confidence >= 0.5
	case RiskLevelMedium:
		return r.Confidence >= 0.6
	case RiskLevelLow:
		return r.Confidence >= 0.8
	default:
		return false
	}
}

func (r ClassificationResult) IsSevere() bool {
	return r.RiskLevel == RiskLevelCritical || (r.RiskLevel == RiskLevelHigh && r.Confidence >= 0.85)
}

// Classifier provides structured content risk assessment.
type Classifier interface {
	Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error)
}
