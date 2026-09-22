package goast

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

func parseFixture(t *testing.T) []evidence.TestEvidence {
	t.Helper()
	source := readFixture(t, "order_test.go")
	parser := NewParser()
	tests, err := parser.ParseTests(context.Background(), "./order_test.go", source)
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	return tests
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return source
}

func byQualified(tests []evidence.TestEvidence, qualified string) (evidence.TestEvidence, bool) {
	for _, test := range tests {
		if test.ID.Qualified == qualified {
			return test, true
		}
	}
	return evidence.TestEvidence{}, false
}

func hasFeature(test evidence.TestEvidence, name string) bool {
	for _, feature := range test.Features {
		if feature.Name == name {
			return true
		}
	}
	return false
}

func TestParseTestsRecoversEveryTestIdentity(t *testing.T) {
	tests := parseFixture(t)
	want := []string{
		"TestCreateOrder",
		"TestCreateOrder/database_failure",
		"TestCreateOrder/tc.name",
		"TestGoldenOutput",
		"FuzzParseName",
		"BenchmarkParseName",
	}
	if len(tests) != len(want) {
		t.Fatalf("got %d tests, want %d: %+v", len(tests), len(want), tests)
	}
	for i, qualified := range want {
		if tests[i].ID.Qualified != qualified {
			t.Fatalf("test[%d] qualified = %q, want %q", i, tests[i].ID.Qualified, qualified)
		}
		if tests[i].ID.Path != "./order_test.go" {
			t.Fatalf("test[%d] path = %q", i, tests[i].ID.Path)
		}
		if tests[i].Language != evidence.LanguageGo {
			t.Fatalf("test[%d] language = %q", i, tests[i].Language)
		}
	}
}

func TestParseTestsRecoversImportsOnce(t *testing.T) {
	tests := parseFixture(t)
	wantImports := []string{
		"database/sql",
		"testing",
		"github.com/stretchr/testify/require",
		"github.com/testcontainers/testcontainers-go",
	}
	for _, test := range tests {
		if len(test.Imports) != len(wantImports) {
			t.Fatalf("%s imports = %v, want %v", test.ID.Qualified, test.Imports, wantImports)
		}
		for i, want := range wantImports {
			if test.Imports[i] != want {
				t.Fatalf("%s imports[%d] = %q, want %q", test.ID.Qualified, i, test.Imports[i], want)
			}
		}
	}
}

func TestParseTestsDetectsDeterministicFeatures(t *testing.T) {
	tests := parseFixture(t)
	parent, ok := byQualified(tests, "TestCreateOrder")
	if !ok {
		t.Fatal("parent test not found")
	}
	for _, name := range []string{
		evidence.FeatureGoTestFunction,
		evidence.FeatureGoSubtestCall,
		evidence.FeatureGoTableDrivenSubtest,
		evidence.FeatureGoParallelCall,
		"imports testcontainers",
		"imports database/sql",
		"imports testify",
	} {
		if !hasFeature(parent, name) {
			t.Fatalf("parent is missing feature %q: %+v", name, parent.Features)
		}
	}

	subtest, ok := byQualified(tests, "TestCreateOrder/database_failure")
	if !ok {
		t.Fatal("subtest not found")
	}
	if hasFeature(subtest, evidence.FeatureGoParallelCall) {
		t.Fatal("subtest inherited the parent's parallel feature")
	}
	if hasFeature(subtest, evidence.FeatureGoTableDrivenSubtest) {
		t.Fatal("named subtest was marked table-driven")
	}

	table, ok := byQualified(tests, "TestCreateOrder/tc.name")
	if !ok {
		t.Fatal("table-driven subtest not found")
	}
	if !hasFeature(table, evidence.FeatureGoTableDrivenSubtest) {
		t.Fatalf("table-driven subtest is missing its feature: %+v", table.Features)
	}

	golden, _ := byQualified(tests, "TestGoldenOutput")
	if !hasFeature(golden, evidence.FeatureGoldenComparison) {
		t.Fatalf("golden feature missing: %+v", golden.Features)
	}

	fuzz, _ := byQualified(tests, "FuzzParseName")
	if !hasFeature(fuzz, evidence.FeatureGoFuzzFunction) {
		t.Fatalf("fuzz feature missing: %+v", fuzz.Features)
	}

	benchmark, _ := byQualified(tests, "BenchmarkParseName")
	if !hasFeature(benchmark, evidence.FeatureGoBenchmarkFunction) {
		t.Fatalf("benchmark feature missing: %+v", benchmark.Features)
	}
}

func TestParseTestsBoundsSourcePerTest(t *testing.T) {
	tests := parseFixture(t)
	parent, _ := byQualified(tests, "TestCreateOrder")
	if !strings.Contains(parent.Source, "func TestCreateOrder") {
		t.Fatalf("parent source does not contain its declaration: %q", parent.Source)
	}
	if strings.Contains(parent.Source, "func TestGoldenOutput") {
		t.Fatal("parent source leaked a sibling test body")
	}
	subtest, _ := byQualified(tests, "TestCreateOrder/database_failure")
	if !strings.Contains(subtest.Source, "sql.Open") {
		t.Fatalf("subtest source missing its body: %q", subtest.Source)
	}
	if strings.Contains(subtest.Source, "func TestCreateOrder") {
		t.Fatal("subtest source includes the parent declaration")
	}
}

func TestParseTestsTruncatesWithConfiguredLimit(t *testing.T) {
	source := readFixture(t, "order_test.go")
	parser := &Parser{Limits: evidence.Limits{MaxSourceBytes: 96}}
	tests, err := parser.ParseTests(context.Background(), "./order_test.go", source)
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	for _, test := range tests {
		if len(test.Source) > 96 {
			t.Fatalf("%s source length %d exceeds limit", test.ID.Qualified, len(test.Source))
		}
	}
}

func TestParseTestsRejectsInvalidSource(t *testing.T) {
	parser := NewParser()
	_, err := parser.ParseTests(context.Background(), "./broken_test.go", []byte("func Test("))
	if err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestParseTestsHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parser := NewParser()
	if _, err := parser.ParseTests(ctx, "./order_test.go", []byte("package x")); err == nil {
		t.Fatal("expected context cancellation error")
	}
}
