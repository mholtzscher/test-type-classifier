// Package evidence defines the language-independent test evidence that parsers
// extract and that classifiers consume. A TestEvidence value is the only test
// payload intended to leave the machine for classification; it never carries a
// whole source file, only a bounded per-test slice plus its provenance.
package evidence

import "context"

// Language identifies the source language a TestEvidence was extracted from.
type Language string

const (
	LanguageGo     Language = "go"
	LanguageKotlin Language = "kotlin"
)

// TestID identifies one test within a repository. Path is the file path as
// discovered; Qualified is the language-local test name, including any nested
// subtest segments joined with "/".
type TestID struct {
	Path      string
	Qualified string
}

// String renders the identifier used in reports, for example
// "./order_test.go::TestCreateOrder/database_failure".
func (id TestID) String() string {
	return id.Path + "::" + id.Qualified
}

// TestEvidence is the bounded, syntax-derived description of a single test.
type TestEvidence struct {
	ID          TestID
	Language    Language
	Name        string
	Annotations []string
	Imports     []string
	Source      string
	Features    []EvidenceFeature
}

// EvidenceFeature is one deterministic syntax observation about a test. Source
// records which rule produced it so a classification can retain provenance.
type EvidenceFeature struct {
	Name   string
	Source string
}

// Feature source values. "parser" marks a deterministic extraction rule.
const (
	FeatureSourceParser = "parser"
)

// Feature names produced by the parser packages. They are stable identifiers so
// that the deterministic trait mapping in the classification package can rely on
// them instead of prose.
const (
	FeatureGoTestFunction       = "go test function"
	FeatureGoFuzzFunction       = "go fuzz function"
	FeatureGoBenchmarkFunction  = "go benchmark function"
	FeatureGoSubtestCall        = "calls t.Run"
	FeatureGoTableDrivenSubtest = "go table-driven subtest loop"
	FeatureGoParallelCall       = "calls t.Parallel"
	FeatureGoldenComparison     = "golden comparison call"
	FeatureSnapshotAssertion    = "snapshot assertion call"

	FeatureKotlinTestAnnotation          = "kotlin @Test annotation"
	FeatureKotlinParameterizedAnnotation = "kotlin @ParameterizedTest annotation"
	FeatureKotlinParameterizedSource     = "kotlin parameterized source annotation"
	FeatureKotlinRepeatedAnnotation      = "kotlin @RepeatedTest annotation"
	FeatureKotlinDynamicTestAnnotation   = "kotlin @TestFactory annotation"
)

// TestParser extracts individual tests and syntax evidence from one file without
// resolving dependencies or compiler symbols.
type TestParser interface {
	ParseTests(ctx context.Context, path string, source []byte) ([]TestEvidence, error)
}
