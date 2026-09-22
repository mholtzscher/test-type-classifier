package jev

import (
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
)

func TestValidatePrimaryAnswerAcceptsDeclaredChoiceWithRoundedProbabilities(t *testing.T) {
	confidence := 0.02
	answer := apiAnswer{
		Type:       "choice",
		Choice:     string(classification.PrimaryIntegration),
		Confidence: &confidence,
		Probabilities: map[string]float64{
			string(classification.PrimaryUnit):        0.34,
			string(classification.PrimaryIntegration): 0.33,
			string(classification.PrimaryUnknown):     0.33,
		},
	}

	primary, _, _, err := validatePrimaryAnswer(answer)
	if err != nil {
		t.Fatal(err)
	}
	if primary != classification.PrimaryIntegration {
		t.Fatalf("primary = %q, want integration", primary)
	}
}
