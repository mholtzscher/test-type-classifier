package classification

import (
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

func TestPrimaryTestTypesAreClosedAndOrdered(t *testing.T) {
	want := []PrimaryTestType{
		PrimaryUnit, PrimaryIntegration, PrimaryContract, PrimaryEndToEnd,
		PrimaryPerformance, PrimarySmoke, PrimaryOther, PrimaryUnknown,
	}
	got := PrimaryTestTypes()
	if len(got) != len(want) {
		t.Fatalf("got %d primary types, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("primary type[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if PrimaryTestTypes()[0] != PrimaryUnit {
		t.Fatal("PrimaryTestTypes returned the backing slice")
	}
}

func TestTestTraitsAreClosedAndOrdered(t *testing.T) {
	want := []TestTrait{
		TraitProperty, TraitFuzz, TraitRegression, TraitGolden, TraitSnapshot,
		TraitParameterized, TraitConcurrency, TraitDatabase, TraitFilesystem,
		TraitNetwork, TraitContainerized, TraitTimeSensitive,
	}
	got := TestTraits()
	if len(got) != len(want) {
		t.Fatalf("got %d traits, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trait[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseRejectsUnknownLabels(t *testing.T) {
	if _, ok := ParsePrimaryTestType("functional"); ok {
		t.Fatal("unknown primary type was accepted")
	}
	if _, ok := ParseTestTrait("flaky"); ok {
		t.Fatal("unknown trait was accepted")
	}
	if got, ok := ParsePrimaryTestType("end_to_end"); !ok || got != PrimaryEndToEnd {
		t.Fatalf("end_to_end not parsed: %q %v", got, ok)
	}
}

func TestTraitPartitionsAreDisjointAndComplete(t *testing.T) {
	deterministic := make(map[TestTrait]bool)
	for _, trait := range DeterministicTraits() {
		deterministic[trait] = true
	}
	nonDeterministic := make(map[TestTrait]bool)
	for _, trait := range NonDeterministicTraits() {
		if deterministic[trait] {
			t.Fatalf("trait %q is both deterministic and non-deterministic", trait)
		}
		nonDeterministic[trait] = true
	}
	for _, trait := range TestTraits() {
		if !deterministic[trait] && !nonDeterministic[trait] {
			t.Fatalf("trait %q is in neither partition", trait)
		}
	}
	if len(deterministic)+len(nonDeterministic) != len(TestTraits()) {
		t.Fatal("partitions do not cover the trait vocabulary exactly once")
	}
}

func TestParserTraitsRetainParserProvenance(t *testing.T) {
	ev := evidence.TestEvidence{
		Features: []evidence.EvidenceFeature{
			{Name: evidence.FeatureGoParallelCall, Source: evidence.FeatureSourceParser},
			{Name: evidence.FeatureGoFuzzFunction, Source: evidence.FeatureSourceParser},
		},
	}
	traits := ParserTraits(ev)
	for _, trait := range []TestTrait{TraitConcurrency, TraitFuzz} {
		got, ok := traits[trait]
		if !ok {
			t.Fatalf("trait %q not detected", trait)
		}
		if !got.Present || got.Source != TraitSourceParser || got.Probability != 1 {
			t.Fatalf("trait %q = %+v", trait, got)
		}
	}
	if _, ok := traits[TraitSnapshot]; ok {
		t.Fatal("snapshot should not be present")
	}
}

func TestPredictionMarginAndTopProbability(t *testing.T) {
	prediction := Prediction{
		Probabilities: map[PrimaryTestType]float64{
			PrimaryUnit:        0.6,
			PrimaryIntegration: 0.3,
			PrimarySmoke:       0.1,
		},
	}
	if got := prediction.TopProbability(); got != 0.6 {
		t.Fatalf("TopProbability = %v", got)
	}
	if got := prediction.Margin(); got < 0.2999 || got > 0.3001 {
		t.Fatalf("Margin = %v, want 0.3", got)
	}
	single := Prediction{Probabilities: map[PrimaryTestType]float64{PrimaryUnit: 1}}
	if got := single.Margin(); got != 0 {
		t.Fatalf("single-option margin = %v, want 0", got)
	}
}
