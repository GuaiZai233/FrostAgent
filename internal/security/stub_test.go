package security

import (
	"context"
	"strings"
)

// ScriptedStub is a deterministic scripted test double for testing security plumbing.
// It lives exclusively in _test.go and is never included in production builds.
type ScriptedStub struct {
	fn func(ctx context.Context, input ClassificationInput) (ClassificationResult, error)
}

func NewScriptedStub(fn func(ctx context.Context, input ClassificationInput) (ClassificationResult, error)) *ScriptedStub {
	return &ScriptedStub{fn: fn}
}

func (s *ScriptedStub) Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error) {
	if s.fn != nil {
		return s.fn(ctx, input)
	}
	text := strings.ToLower(input.Normalized)
	if strings.Contains(text, "ignore all previous") ||
		strings.Contains(text, "disable the safety") ||
		strings.Contains(text, "cat /etc/passwd") ||
		strings.Contains(text, "cat /etc/shadow") ||
		strings.Contains(text, "whoami") ||
		strings.Contains(text, "curl http") ||
		strings.Contains(text, "wget http") ||
		strings.Contains(text, "rm -rf") ||
		strings.Contains(text, "malicious") ||
		strings.Contains(text, "sh -i") ||
		strings.Contains(text, "bash -i") ||
		strings.Contains(text, "nc -e") ||
		strings.Contains(text, "system prompt") {
		return ClassificationResult{
			Category:   RiskCategoryPromptInjection,
			RiskLevel:  RiskLevelHigh,
			Intent:     IntentMalicious,
			Confidence: 0.90,
			Origin:     input.Origin,
			Reason:     "scripted stub policy violation",
		}, nil
	}
	return ClassificationResult{
		Category:   RiskCategoryNone,
		RiskLevel:  RiskLevelNone,
		Intent:     IntentBenign,
		Confidence: 0.0,
		Origin:     input.Origin,
		Reason:     "benign",
	}, nil
}
