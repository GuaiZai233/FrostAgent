package security

import (
	"context"
)

// HybridClassifier coordinates an LLM-based security gateway with a local
// deterministic calibrated fallback to guarantee high robustness and offline resiliency.
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

func (h *HybridClassifier) Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error) {
	if h.llm != nil {
		res, err := h.llm.Classify(ctx, input)
		if err == nil && res.Validate() == nil {
			return res, nil
		}
	}
	if h.fallback == nil {
		h.fallback = NewCalibratedClassifier()
	}
	// Fallback to local calibrated classifier
	return h.fallback.Classify(ctx, input)
}
