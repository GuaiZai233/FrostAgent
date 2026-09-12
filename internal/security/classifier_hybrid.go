package security

import (
	"context"
	"fmt"
)

// HybridClassifier coordinates an LLM-based security gateway with a local
// deterministic calibrated fallback. In production mode (when an LLM classifier is configured),
// classifier errors, timeouts, and validation failures strictly fail-closed (Option A),
// returning an error to Watchdog to ensure content is blocked without penalizing the user.
// In offline or disabled mode (when LLM is nil), the deterministic calibrated classifier is used.
type HybridClassifier struct {
	llm      *LLMClassifier
	fallback *CalibratedClassifier
}

func NewHybridClassifier(llm *LLMClassifier, fallback *CalibratedClassifier) *HybridClassifier {
	if fallback == nil {
		fallback = NewCalibratedClassifier()
	}
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

func (h *HybridClassifier) Fallback() *CalibratedClassifier {
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
	if h.fallback == nil {
		h.fallback = NewCalibratedClassifier()
	}
	// Fallback to local calibrated classifier in offline/no-LLM mode
	return h.fallback.Classify(ctx, input)
}

