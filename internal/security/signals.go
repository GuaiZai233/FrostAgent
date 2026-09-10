package security

import "context"

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

type RiskLevel string

const (
	RiskLevelNone     RiskLevel = "none"
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

type ActorIntent string

const (
	IntentBenign    ActorIntent = "benign"
	IntentAmbiguous ActorIntent = "ambiguous"
	IntentMalicious ActorIntent = "malicious"
)

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
