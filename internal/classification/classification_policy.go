package classification

// ClassificationPolicy converts model uncertainty into accept or review
// decisions.
type ClassificationPolicy interface {
	DecideClassification(prediction Prediction) ReviewDecision
}

// ThresholdPolicy accepts a prediction only when it clears both a confidence and
// a margin threshold. A zero-value policy is deliberately disabled: the default
// is to review everything until thresholds are selected from a labeled
// evaluation, so confidence is never mistaken for correctness.
type ThresholdPolicy struct {
	minConfidence float64
	minMargin     float64
	enabled       bool
}

// NewThresholdPolicy enables a policy with measured thresholds for the minimum
// confidence and the minimum gap between the top two primary probabilities.
func NewThresholdPolicy(minConfidence, minMargin float64) ThresholdPolicy {
	return ThresholdPolicy{minConfidence: minConfidence, minMargin: minMargin, enabled: true}
}

// DefaultPolicy reviews every prediction. It is the policy in force until a
// labeled evaluation supplies thresholds.
func DefaultPolicy() ThresholdPolicy {
	return ThresholdPolicy{}
}

// Enabled reports whether measured thresholds are in force.
func (p ThresholdPolicy) Enabled() bool {
	return p.enabled
}

// MinConfidence returns the configured confidence threshold.
func (p ThresholdPolicy) MinConfidence() float64 {
	return p.minConfidence
}

// MinMargin returns the configured margin threshold.
func (p ThresholdPolicy) MinMargin() float64 {
	return p.minMargin
}

// DecideClassification returns review whenever thresholds are disabled, the
// prediction has no primary label, confidence is below the threshold, or the
// top-two margin is below the threshold. Otherwise it accepts.
func (p ThresholdPolicy) DecideClassification(prediction Prediction) ReviewDecision {
	if !p.enabled {
		return ReviewDecisionReview
	}
	if prediction.Primary == "" {
		return ReviewDecisionReview
	}
	if prediction.Confidence < p.minConfidence {
		return ReviewDecisionReview
	}
	if prediction.Margin() < p.minMargin {
		return ReviewDecisionReview
	}
	return ReviewDecisionAccept
}
