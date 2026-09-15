package security

import (
	"context"
	"fmt"
	"strings"
)

type RiskCategory string

const (
	RiskCategoryNone                   RiskCategory = "none"
	RiskCategoryPromptInjection        RiskCategory = "prompt_injection"
	RiskCategoryMaliciousExecution     RiskCategory = "malicious_execution"
	RiskCategoryDataExfiltration       RiskCategory = "data_exfiltration"
	RiskCategoryPolitics               RiskCategory = "politics"
	RiskCategoryPornography            RiskCategory = "pornography"
	RiskCategoryViolenceTerrorism      RiskCategory = "violence_terrorism"
	RiskCategoryContraband             RiskCategory = "contraband"
	RiskCategoryFraudGambling          RiskCategory = "fraud_gambling"
	RiskCategoryHarassmentManipulation RiskCategory = "harassment_manipulation"
)

func (c RiskCategory) IsValid() bool {
	switch c {
	case RiskCategoryNone,
		RiskCategoryPromptInjection,
		RiskCategoryMaliciousExecution,
		RiskCategoryDataExfiltration,
		RiskCategoryPolitics,
		RiskCategoryPornography,
		RiskCategoryViolenceTerrorism,
		RiskCategoryContraband,
		RiskCategoryFraudGambling,
		RiskCategoryHarassmentManipulation:
		return true
	default:
		return false
	}
}

// NormalizeRiskCategory canonicalizes category strings from LLM or external sources.
func NormalizeRiskCategory(s string) (RiskCategory, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "none":
		return RiskCategoryNone, true
	case "prompt_injection", "promptinjection", "jailbreak":
		return RiskCategoryPromptInjection, true
	case "malicious_execution", "maliciousexecution", "execution", "command_execution":
		return RiskCategoryMaliciousExecution, true
	case "data_exfiltration", "dataexfiltration", "exfiltration", "leak":
		return RiskCategoryDataExfiltration, true
	case "politics", "political", "political_sensitive":
		return RiskCategoryPolitics, true
	case "pornography", "pornography_vulgarity", "vulgarity", "erotic":
		return RiskCategoryPornography, true
	case "violence_terrorism", "violence", "terrorism":
		return RiskCategoryViolenceTerrorism, true
	case "contraband", "illegal_contraband", "contraband_goods":
		return RiskCategoryContraband, true
	case "fraud_gambling", "fraud", "gambling":
		return RiskCategoryFraudGambling, true
	case "harassment_manipulation", "harassment", "manipulation", "abuse":
		return RiskCategoryHarassmentManipulation, true
	case "platform_policy", "platformpolicy", "tencent_compliance", "tencentcompliance":
		// Compatibility mapping for legacy category values: map to most specific or default to prompt_injection / contraband
		return RiskCategoryHarassmentManipulation, true
	default:
		return RiskCategory(s), false
	}
}

type RiskLevel string

const (
	RiskLevelNone     RiskLevel = "none"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

func (l RiskLevel) IsValid() bool {
	switch l {
	case RiskLevelNone, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return true
	default:
		return false
	}
}

// NormalizeRiskLevel canonicalizes risk level strings.
func NormalizeRiskLevel(s string) (RiskLevel, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "none":
		return RiskLevelNone, true
	case "low":
		// Legacy low is normalized to medium
		return RiskLevelMedium, true
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

// ClassificationInput carries content, provenance, and historical context
// required for calibrated policy evaluation.
type ClassificationInput struct {
	EvaluationID    string         `json:"evaluation_id,omitempty"`
	Instance        string         `json:"instance,omitempty"`
	Session         string         `json:"session,omitempty"`
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
	Category  RiskCategory   `json:"category"`
	RiskLevel RiskLevel      `json:"risk_level"`
	Origin    WatchdogSource `json:"origin"`
	Reason    string         `json:"reason"`
	Details   []string       `json:"details,omitempty"`
}

func (r ClassificationResult) Validate() error {
	if !r.Category.IsValid() {
		return fmt.Errorf("invalid risk category: %q", r.Category)
	}
	if !r.RiskLevel.IsValid() {
		return fmt.Errorf("invalid risk level: %q", r.RiskLevel)
	}
	// Invariant consistency checks: category == none <=> risk_level == none
	if r.Category == RiskCategoryNone {
		if r.RiskLevel != RiskLevelNone {
			return fmt.Errorf("category none requires risk_level none, got %q", r.RiskLevel)
		}
	} else {
		if r.RiskLevel == RiskLevelNone {
			return fmt.Errorf("non-none category %q cannot have risk_level none", r.Category)
		}
	}
	return nil
}

func (r ClassificationResult) IsRisky() bool {
	return r.Category != RiskCategoryNone && r.RiskLevel != RiskLevelNone
}

func (r ClassificationResult) IsSevere() bool {
	return r.RiskLevel == RiskLevelCritical
}

// Classifier provides structured content risk assessment.
type Classifier interface {
	Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error)
}
