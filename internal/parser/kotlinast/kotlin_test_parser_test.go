package kotlinast

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
	source, err := os.ReadFile(filepath.Join("testdata", "OrderTest.kt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	parser := NewParser()
	tests, err := parser.ParseTests(context.Background(), "./OrderTest.kt", source)
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	return tests
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

func hasAnnotation(test evidence.TestEvidence, want string) bool {
	for _, annotation := range test.Annotations {
		if annotation == want {
			return true
		}
	}
	return false
}

func TestParseTestsRecoversJUnitAndParameterizedTests(t *testing.T) {
	tests := parseFixture(t)
	want := []string{
		"OrderTest.creates order",
		"OrderTest.totals items",
		"LegacyTest.checksThing",
	}
	if len(tests) != len(want) {
		t.Fatalf("got %d tests, want %d: %+v", len(tests), len(want), tests)
	}
	for i, qualified := range want {
		if tests[i].ID.Qualified != qualified {
			t.Fatalf("test[%d] qualified = %q, want %q", i, tests[i].ID.Qualified, qualified)
		}
		if tests[i].Language != evidence.LanguageKotlin {
			t.Fatalf("test[%d] language = %q", i, tests[i].Language)
		}
		if tests[i].ID.Path != "./OrderTest.kt" {
			t.Fatalf("test[%d] path = %q", i, tests[i].ID.Path)
		}
	}
}

func TestParseTestsRecoversAnnotationsAndImports(t *testing.T) {
	tests := parseFixture(t)
	creates, ok := byQualified(tests, "OrderTest.creates order")
	if !ok {
		t.Fatal("creates order not found")
	}
	if !hasAnnotation(creates, "@Test") {
		t.Fatalf("creates order annotations = %v", creates.Annotations)
	}

	parameterized, ok := byQualified(tests, "OrderTest.totals items")
	if !ok {
		t.Fatal("totals items not found")
	}
	if !hasAnnotation(parameterized, `@ParameterizedTest(name = "order {0}")`) {
		t.Fatalf("parameterized annotations = %v", parameterized.Annotations)
	}
	if !hasAnnotation(parameterized, `@CsvSource("1, 1", "2, 2")`) {
		t.Fatalf("parameterized annotations = %v", parameterized.Annotations)
	}

	wantImports := []string{
		"org.junit.jupiter.api.Test",
		"org.junit.jupiter.params.ParameterizedTest",
		"org.junit.jupiter.params.provider.CsvSource",
		"org.testcontainers.containers.PostgreSQLContainer",
	}
	for i, want := range wantImports {
		if creates.Imports[i] != want {
			t.Fatalf("imports[%d] = %q, want %q", i, creates.Imports[i], want)
		}
	}
}

func TestParseTestsStripsKotlinImportAliases(t *testing.T) {
	source := []byte("" +
		"import kotlinx.coroutines.runBlocking as run\n" +
		"import org.junit.jupiter.api.Test\n" +
		"class AliasTest { @Test fun works() = run { } }\n")
	tests, err := NewParser().ParseTests(context.Background(), "AliasTest.kt", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) != 1 {
		t.Fatalf("tests = %d, want 1", len(tests))
	}
	if got, want := tests[0].Imports[0], "kotlinx.coroutines.runBlocking"; got != want {
		t.Fatalf("aliased import = %q, want %q", got, want)
	}
}

func TestParseTestsDetectsDeterministicFeatures(t *testing.T) {
	tests := parseFixture(t)
	creates, _ := byQualified(tests, "OrderTest.creates order")
	for _, name := range []string{
		evidence.FeatureKotlinTestAnnotation,
		"imports junit jupiter",
		"imports junit params",
		"imports testcontainers",
	} {
		if !hasFeature(creates, name) {
			t.Fatalf("creates order missing %q: %+v", name, creates.Features)
		}
	}
	if hasFeature(creates, evidence.FeatureKotlinParameterizedAnnotation) {
		t.Fatal("plain @Test was marked parameterized")
	}

	parameterized, _ := byQualified(tests, "OrderTest.totals items")
	for _, name := range []string{
		evidence.FeatureKotlinParameterizedAnnotation,
		evidence.FeatureKotlinParameterizedSource,
	} {
		if !hasFeature(parameterized, name) {
			t.Fatalf("parameterized test missing %q: %+v", name, parameterized.Features)
		}
	}

	legacy, _ := byQualified(tests, "LegacyTest.checksThing")
	if !hasFeature(legacy, evidence.FeatureGoldenComparison) {
		t.Fatalf("golden feature missing: %+v", legacy.Features)
	}
}

func TestParseTestsBoundsSourcePerTest(t *testing.T) {
	tests := parseFixture(t)
	creates, _ := byQualified(tests, "OrderTest.creates order")
	if !strings.Contains(creates.Source, "fun `creates order`") {
		t.Fatalf("source missing declaration: %q", creates.Source)
	}
	if strings.Contains(creates.Source, "totals items") {
		t.Fatal("source leaked a sibling test body")
	}
	if strings.Contains(creates.Source, "class OrderTest") {
		t.Fatalf("source includes the class declaration: %q", creates.Source)
	}
}

func TestParseTestsHandlesTopLevelExpressionBodyAndNestedClass(t *testing.T) {
	source := strings.Join([]string{
		"package demo",
		"",
		"import org.junit.jupiter.api.Test",
		"",
		"@file:JvmName(\"Demo\")",
		"",
		"@Test",
		"fun topLevel() {",
		"}",
		"",
		"@Test fun expression() = assertEquals(1, 1)",
		"",
		"class Outer {",
		"    class Inner {",
		"        @Test",
		"        fun nested() {}",
		"    }",
		"}",
		"",
	}, "\n")
	parser := NewParser()
	tests, err := parser.ParseTests(context.Background(), "./Demo.kt", []byte(source))
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	names := make([]string, 0, len(tests))
	for _, test := range tests {
		names = append(names, test.ID.Qualified)
	}
	want := []string{"topLevel", "expression", "Outer.Inner.nested"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	expression, _ := byQualified(tests, "expression")
	if !strings.Contains(expression.Source, "assertEquals(1, 1)") {
		t.Fatalf("expression body source = %q", expression.Source)
	}
}

func TestParseTestsHandlesGenericFunctionsAndClassHeaders(t *testing.T) {
	source := strings.Join([]string{
		"package demo",
		"",
		"import org.junit.jupiter.api.Test",
		"",
		"class Empty",
		"",
		"class Repo<T>(private val db: T) : Base(), Closeable {",
		"    @Test",
		"    fun <T> works(value: List<T>) {",
		"    }",
		"}",
		"",
		"object Registry {",
		"    @Test fun registered() {}",
		"}",
		"",
	}, "\n")
	parser := NewParser()
	tests, err := parser.ParseTests(context.Background(), "./Demo.kt", []byte(source))
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	names := make([]string, 0, len(tests))
	for _, test := range tests {
		names = append(names, test.ID.Qualified)
	}
	want := []string{"Repo.works", "Registry.registered"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestParseTestsTruncatesWithConfiguredLimit(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "OrderTest.kt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	parser := &Parser{Limits: evidence.Limits{MaxSourceBytes: 80}}
	tests, err := parser.ParseTests(context.Background(), "./OrderTest.kt", source)
	if err != nil {
		t.Fatalf("ParseTests: %v", err)
	}
	if len(tests) == 0 {
		t.Fatal("no tests extracted")
	}
	for _, test := range tests {
		if len(test.Source) > 80 {
			t.Fatalf("%s source length %d exceeds limit", test.ID.Qualified, len(test.Source))
		}
	}
}

func TestParseTestsHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parser := NewParser()
	if _, err := parser.ParseTests(ctx, "./Demo.kt", []byte("package demo")); err == nil {
		t.Fatal("expected context cancellation error")
	}
}
