// Package classification holds the closed test taxonomy, the deterministic
// trait mapping, and the confidence policy that turns a model prediction into an
// accept or review decision.
package classification

import (
	"context"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// PrimaryTestType is the single execution-boundary label assigned to a test.
type PrimaryTestType string

const (
	// PrimaryUnit exercises a small in-process scope with external boundaries
	// replaced or absent.
	PrimaryUnit PrimaryTestType = "unit"
	// PrimaryIntegration exercises a real boundary or joins production components.
	PrimaryIntegration PrimaryTestType = "integration"
	// PrimaryContract verifies an independently maintained consumer-provider,
	// protocol, schema, or compatibility contract.
	PrimaryContract PrimaryTestType = "contract"
	// PrimaryEndToEnd drives a complete workflow through a public entry point
	// and real downstream boundaries.
	PrimaryEndToEnd PrimaryTestType = "end_to_end"
	// PrimaryPerformance measures latency, throughput, allocation, or load as
	// its main purpose.
	PrimaryPerformance PrimaryTestType = "performance"
	// PrimarySmoke performs a shallow availability or startup check.
	PrimarySmoke PrimaryTestType = "smoke"
	// PrimaryOther is a recognizable test that fits none of the definitions.
	PrimaryOther PrimaryTestType = "other"
	// PrimaryUnknown means the evidence does not support a reliable label.
	PrimaryUnknown PrimaryTestType = "unknown"
)

// TestTrait is an independent property of a test. Traits never replace the
// primary boundary type; a database-backed regression test is integration with
// database and regression traits.
type TestTrait string

const (
	TraitProperty      TestTrait = "property"
	TraitFuzz          TestTrait = "fuzz"
	TraitRegression    TestTrait = "regression"
	TraitGolden        TestTrait = "golden"
	TraitSnapshot      TestTrait = "snapshot"
	TraitParameterized TestTrait = "parameterized"
	TraitConcurrency   TestTrait = "concurrency"
	TraitDatabase      TestTrait = "database"
	TraitFilesystem    TestTrait = "filesystem"
	TraitNetwork       TestTrait = "network"
	TraitContainerized TestTrait = "containerized"
	TraitTimeSensitive TestTrait = "time_sensitive"
)

// Trait source values record where a trait decision came from. Deterministic
// parser rules and Jev answers are combined while keeping their provenance.
const (
	TraitSourceParser   = "parser"
	TraitSourceJev      = "jev"
	TraitSourceCombined = "combined"
)

// TraitPresenceThreshold is the Noul probability at or above which a
// non-deterministic trait is treated as present.
const TraitPresenceThreshold = 0.5

// TaxonomyVersion identifies the closed primary-type and trait vocabulary.
// Change it whenever a label is added, removed, or redefined.
const TaxonomyVersion = "1.0.0"

var primaryTestTypes = []PrimaryTestType{
	PrimaryUnit,
	PrimaryIntegration,
	PrimaryContract,
	PrimaryEndToEnd,
	PrimaryPerformance,
	PrimarySmoke,
	PrimaryOther,
	PrimaryUnknown,
}

var testTraits = []TestTrait{
	TraitProperty,
	TraitFuzz,
	TraitRegression,
	TraitGolden,
	TraitSnapshot,
	TraitParameterized,
	TraitConcurrency,
	TraitDatabase,
	TraitFilesystem,
	TraitNetwork,
	TraitContainerized,
	TraitTimeSensitive,
}

// primaryTestTypeSet backs ParsePrimaryTestType.
var primaryTestTypeSet = func() map[string]PrimaryTestType {
	m := make(map[string]PrimaryTestType, len(primaryTestTypes))
	for _, t := range primaryTestTypes {
		m[string(t)] = t
	}
	return m
}()

var testTraitSet = func() map[string]TestTrait {
	m := make(map[string]TestTrait, len(testTraits))
	for _, t := range testTraits {
		m[string(t)] = t
	}
	return m
}()

// PrimaryTestTypes returns the closed primary-type vocabulary in display order.
func PrimaryTestTypes() []PrimaryTestType {
	return append([]PrimaryTestType(nil), primaryTestTypes...)
}

// TestTraits returns the closed trait vocabulary in display order.
func TestTraits() []TestTrait {
	return append([]TestTrait(nil), testTraits...)
}

// ParsePrimaryTestType validates a label against the closed vocabulary.
func ParsePrimaryTestType(value string) (PrimaryTestType, bool) {
	t, ok := primaryTestTypeSet[value]
	return t, ok
}

// ParseTestTrait validates a trait against the closed vocabulary.
func ParseTestTrait(value string) (TestTrait, bool) {
	t, ok := testTraitSet[value]
	return t, ok
}

// DeterministicTraits returns the traits that parser rules can set without a
// semantic judgment.
func DeterministicTraits() []TestTrait {
	return []TestTrait{TraitFuzz, TraitGolden, TraitSnapshot, TraitParameterized, TraitConcurrency}
}

// NonDeterministicTraits returns the traits that need a Jev question.
func NonDeterministicTraits() []TestTrait {
	deterministic := make(map[TestTrait]bool, len(DeterministicTraits()))
	for _, t := range DeterministicTraits() {
		deterministic[t] = true
	}
	var traits []TestTrait
	for _, t := range testTraits {
		if !deterministic[t] {
			traits = append(traits, t)
		}
	}
	return traits
}

// TraitPrediction is the combined parser and model judgment for one trait.
type TraitPrediction struct {
	Present     bool
	Probability float64
	Source      string
}

// Prediction is a classifier result for one test.
type Prediction struct {
	Primary       PrimaryTestType
	Probabilities map[PrimaryTestType]float64
	Confidence    float64
	Traits        map[TestTrait]TraitPrediction
	Model         string
}

// TopProbability returns the highest primary-type probability.
func (p Prediction) TopProbability() float64 {
	top := 0.0
	for _, probability := range p.Probabilities {
		if probability > top {
			top = probability
		}
	}
	return top
}

// Margin returns the gap between the top two primary-type probabilities. A
// prediction with fewer than two options has a margin of zero, which routes it
// to review under any positive margin threshold.
func (p Prediction) Margin() float64 {
	first, second := 0.0, 0.0
	count := 0
	for _, probability := range p.Probabilities {
		count++
		switch {
		case probability > first:
			second = first
			first = probability
		case probability > second:
			second = probability
		}
	}
	if count < 2 {
		return 0
	}
	return first - second
}

// ReviewDecision is the policy outcome for a prediction.
type ReviewDecision string

const (
	// ReviewDecisionAccept means the prediction may be used automatically.
	ReviewDecisionAccept ReviewDecision = "accept"
	// ReviewDecisionReview means a person must inspect the prediction.
	ReviewDecisionReview ReviewDecision = "review"
)

// Classification pairs evidence with its prediction and policy decision.
type Classification struct {
	Evidence   evidence.TestEvidence
	Prediction Prediction
	Decision   ReviewDecision
}

// TestClassifier predicts one boundary type and independent traits for test
// evidence. It accepts a slice so the implementation can control concurrency and
// request limits.
type TestClassifier interface {
	ClassifyTests(ctx context.Context, tests []evidence.TestEvidence) ([]Prediction, error)
}
