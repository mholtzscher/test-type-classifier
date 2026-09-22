package classification

import "testing"

func highConfidencePrediction() Prediction {
	return Prediction{
		Primary: PrimaryIntegration,
		Probabilities: map[PrimaryTestType]float64{
			PrimaryIntegration: 0.9,
			PrimaryUnit:        0.05,
			PrimaryEndToEnd:    0.05,
		},
		Confidence: 0.9,
	}
}

func TestDefaultPolicyReviewsEverything(t *testing.T) {
	policy := DefaultPolicy()
	if policy.Enabled() {
		t.Fatal("default policy must not be enabled before thresholds are measured")
	}
	if got := policy.DecideClassification(highConfidencePrediction()); got != ReviewDecisionReview {
		t.Fatalf("default decision = %q, want review", got)
	}
}

func TestThresholdPolicyAcceptsConfidentWideMargin(t *testing.T) {
	policy := NewThresholdPolicy(0.8, 0.5)
	if got := policy.DecideClassification(highConfidencePrediction()); got != ReviewDecisionAccept {
		t.Fatalf("decision = %q, want accept", got)
	}
}

func TestThresholdPolicyReviewsLowConfidence(t *testing.T) {
	prediction := highConfidencePrediction()
	prediction.Confidence = 0.4
	policy := NewThresholdPolicy(0.8, 0.5)
	if got := policy.DecideClassification(prediction); got != ReviewDecisionReview {
		t.Fatalf("decision = %q, want review", got)
	}
}

func TestThresholdPolicyReviewsNarrowMargin(t *testing.T) {
	prediction := Prediction{
		Primary: PrimaryUnit,
		Probabilities: map[PrimaryTestType]float64{
			PrimaryUnit:        0.51,
			PrimaryIntegration: 0.49,
		},
		Confidence: 1,
	}
	policy := NewThresholdPolicy(0.8, 0.1)
	if got := policy.DecideClassification(prediction); got != ReviewDecisionReview {
		t.Fatalf("decision = %q, want review", got)
	}
}

func TestThresholdPolicyReviewsEmptyPrediction(t *testing.T) {
	policy := NewThresholdPolicy(0, 0)
	if got := policy.DecideClassification(Prediction{}); got != ReviewDecisionReview {
		t.Fatalf("decision = %q, want review", got)
	}
}

func TestThresholdPolicyExposesThresholds(t *testing.T) {
	policy := NewThresholdPolicy(0.75, 0.25)
	if policy.MinConfidence() != 0.75 || policy.MinMargin() != 0.25 {
		t.Fatalf("thresholds = %v %v", policy.MinConfidence(), policy.MinMargin())
	}
}
