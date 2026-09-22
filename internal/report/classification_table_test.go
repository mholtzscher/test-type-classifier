package report

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

func classificationFor(path, qualified string, primary classification.PrimaryTestType, confidence float64, traits ...classification.TestTrait) classification.Classification {
	traitPredictions := make(map[classification.TestTrait]classification.TraitPrediction, len(traits))
	for _, trait := range traits {
		traitPredictions[trait] = classification.TraitPrediction{Present: true, Probability: 1, Source: classification.TraitSourceParser}
	}
	return classification.Classification{
		Evidence: evidence.TestEvidence{ID: evidence.TestID{Path: path, Qualified: qualified}},
		Prediction: classification.Prediction{
			Primary:    primary,
			Confidence: confidence,
			Traits:     traitPredictions,
		},
		Decision: classification.ReviewDecisionReview,
	}
}

func TestWriteReportSortsRowsAndTraits(t *testing.T) {
	results := []classification.Classification{
		classificationFor("b_test.go", "TestB", classification.PrimaryUnit, 0.5,
			classification.TraitParameterized, classification.TraitDatabase),
		classificationFor("a_test.go", "TestA", classification.PrimaryIntegration, 0.876),
	}
	var out bytes.Buffer
	if err := NewTableReporter(&out).WriteReport(context.Background(), results); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	rendered := out.String()
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d:\n%s", len(lines), rendered)
	}
	if !strings.HasPrefix(lines[0], "TYPE") || !strings.Contains(lines[0], "TEST") {
		t.Fatalf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "a_test.go::TestA") || !strings.Contains(lines[1], "integration") || !strings.Contains(lines[1], "0.88") {
		t.Fatalf("first row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "b_test.go::TestB") {
		t.Fatalf("second row = %q", lines[2])
	}
	if !strings.Contains(lines[2], "database,parameterized") {
		t.Fatalf("traits were not sorted: %q", lines[2])
	}
}

func TestWriteReportIsDeterministic(t *testing.T) {
	results := []classification.Classification{
		classificationFor("b_test.go", "TestB", classification.PrimaryUnit, 0.51, classification.TraitFuzz),
		classificationFor("a_test.go", "TestA", classification.PrimarySmoke, 0.12, classification.TraitNetwork),
	}
	var first, second bytes.Buffer
	if err := NewTableReporter(&first).WriteReport(context.Background(), results); err != nil {
		t.Fatalf("WriteReport first: %v", err)
	}
	if err := NewTableReporter(&second).WriteReport(context.Background(), results); err != nil {
		t.Fatalf("WriteReport second: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("output not deterministic:\n%s\n---\n%s", first.String(), second.String())
	}
}

func TestWriteReportEmptyHasHeaderOnly(t *testing.T) {
	var out bytes.Buffer
	if err := NewTableReporter(&out).WriteReport(context.Background(), nil); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if !strings.HasPrefix(out.String(), "TYPE") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("empty report = %q", out.String())
	}
}

func TestWriteReportHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := NewTableReporter(&out).WriteReport(ctx, nil); err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestWriteSkippedFilesSortsAndFormats(t *testing.T) {
	var out bytes.Buffer
	if err := WriteSkippedFiles(&out, []SkippedFile{
		{Path: "b_test.go", Reason: "boom"},
		{Path: "a_test.go", Reason: "bad syntax"},
	}); err != nil {
		t.Fatalf("WriteSkippedFiles: %v", err)
	}
	want := "SKIPPED FILES\n  a_test.go: bad syntax\n  b_test.go: boom\n"
	if out.String() != want {
		t.Fatalf("skipped output = %q, want %q", out.String(), want)
	}
}

func TestWriteSkippedFilesEmptyWritesNothing(t *testing.T) {
	var out bytes.Buffer
	if err := WriteSkippedFiles(&out, nil); err != nil {
		t.Fatalf("WriteSkippedFiles: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}
