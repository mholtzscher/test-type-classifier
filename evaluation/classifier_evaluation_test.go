package main

import (
	"strings"
	"testing"
)

func TestSummarizeEvaluationReportsBoundaryTraitAndAcceptanceMetrics(t *testing.T) {
	records := []evaluationRecord{
		{Language: "go", Repository: "a", Split: "development", HumanPrimary: "unit", PredictedPrimary: "unit", ReviewerAPrimary: "unit", ReviewerBPrimary: "unit", HumanTraits: []string{"parameterized"}, PredictedTraits: []string{"parameterized"}, Confidence: .9, PrimaryProbabilities: map[string]float64{"unit": .8, "integration": .2}},
		{Language: "kotlin", Repository: "b", Split: "holdout", HumanPrimary: "integration", PredictedPrimary: "unit", ReviewerAPrimary: "integration", ReviewerBPrimary: "unit", HumanTraits: []string{"database"}, PredictedTraits: []string{"network"}, Confidence: .8, PrimaryProbabilities: map[string]float64{"unit": .55, "integration": .45}},
	}

	summary, err := summarizeEvaluation(records, .85, .3)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Accepted != 1 || summary.AcceptedCorrect != 1 {
		t.Fatalf("accepted metrics = %d/%d, want 1/1", summary.AcceptedCorrect, summary.Accepted)
	}
	if got := summary.Primary["unit"]; got.TruePositive != 1 || got.FalsePositive != 1 {
		t.Fatalf("unit metrics = %+v", got)
	}
	if got := summary.Traits["database"]; got.FalseNegative != 1 {
		t.Fatalf("database metrics = %+v", got)
	}
}

func TestReadEvaluationRecordsExcludesPendingSeedRows(t *testing.T) {
	input := strings.NewReader("{\"id\":\"pending\",\"pending_human_review\":true}\n" +
		"{\"id\":\"reviewed\",\"repository\":\"r\",\"split\":\"holdout\",\"language\":\"go\",\"human_primary\":\"unit\",\"predicted_primary\":\"unit\",\"model\":\"jev-1.13.0\",\"question_set_version\":\"1\",\"extractor_version\":\"1\",\"reviewer_a_primary\":\"unit\",\"reviewer_b_primary\":\"unit\"}\n")
	records, err := readEvaluationRecords(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "reviewed" {
		t.Fatalf("records = %+v", records)
	}
}

func TestSummarizeEvaluationDisablesAcceptanceWithoutThresholds(t *testing.T) {
	record := evaluationRecord{Language: "go", Repository: "a", Split: "holdout", HumanPrimary: "unit", PredictedPrimary: "unit", ReviewerAPrimary: "unit", ReviewerBPrimary: "unit", Confidence: 1, PrimaryProbabilities: map[string]float64{"unit": 1, "unknown": 0}}
	summary, err := summarizeEvaluation([]evaluationRecord{record}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Accepted != 0 {
		t.Fatalf("accepted = %d, want review-by-default", summary.Accepted)
	}
}

func TestCohensKappa(t *testing.T) {
	if got := cohensKappa([]string{"unit", "integration", "unit", "integration"}, []string{"unit", "integration", "unit", "integration"}); got != 1 {
		t.Fatalf("perfect kappa = %v, want 1", got)
	}
}

func TestProbabilityMarginRequiresTwoProbabilities(t *testing.T) {
	if got := probabilityMargin(map[string]float64{"unit": 1}); got != 0 {
		t.Fatalf("margin = %v, want 0", got)
	}
	if got := probabilityMargin(map[string]float64{"unit": .75, "integration": .2, "unknown": .05}); got != .55 {
		t.Fatalf("margin = %v, want .55", got)
	}
}
