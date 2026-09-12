package security

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoLLMProvider indicates that no LLM security gateway provider is configured.
var ErrNoLLMProvider = errors.New("llm security provider not configured")

// HybridClassifier coordinates an LLM-based security gateway with an optional fallback classifier.
// In the LLM-only architecture, if no LLM classifier and no fallback is configured,
// Classify returns ErrNoLLMProvider to ensure strict Option A fail-closed behavior.
type HybridClassifier struct {
	llm      *LLMClassifier
	fallback Classifier
}

func NewHybridClassifier(llm *LLMClassifier, fallback Classifier) *HybridClassifier {
	return &HybridClassifier{
		llm:      llm,
		fallback: fallback,
	}
}

func (h *HybridClassifier) SetLLM(llm *LLMClassifier) {
	h.llm = llm
}

func (h *HybridClassifier) LLM() *LLMClassifier {
	return h.llm
}

func (h *HybridClassifier) Fallback() Classifier {
	return h.fallback
}

func (h *HybridClassifier) Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error) {
	if h.llm != nil {
		res, err := h.llm.Classify(ctx, input)
		if err != nil {
			return ClassificationResult{}, fmt.Errorf("hybrid llm gateway error: %w", err)
		}
		return res, nil
	}
	if h.fallback != nil {
		return h.fallback.Classify(ctx, input)
	}
	return ClassificationResult{}, ErrNoLLMProvider
}

