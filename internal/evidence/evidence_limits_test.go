package evidence

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateSourceKeepsShortSource(t *testing.T) {
	source := "func TestThing(t *testing.T) {}"
	if got := TruncateSource(source, 1024); got != source {
		t.Fatalf("short source was modified: %q", got)
	}
}

func TestTruncateSourceBoundsBytesAndStaysUTF8(t *testing.T) {
	source := strings.Repeat("é", 100) // two bytes per rune
	maxBytes := 51
	got := TruncateSource(source, maxBytes)
	if len(got) > maxBytes {
		t.Fatalf("truncated length %d exceeds limit %d", len(got), maxBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated source is not valid UTF-8")
	}
	if !strings.HasSuffix(got, SourceTruncationMarker) {
		t.Fatalf("truncated source is missing the marker: %q", got)
	}
}

func TestTruncateSourceTinyLimitDoesNotPanic(t *testing.T) {
	got := TruncateSource("abcdef", 2)
	if !utf8.ValidString(got) || len(got) > 2 {
		t.Fatalf("unexpected tiny truncation: %q", got)
	}
}

func TestLimitsApplyCapsEveryField(t *testing.T) {
	limits := Limits{
		MaxSourceBytes: 16,
		MaxImports:     1,
		MaxAnnotations: 1,
		MaxFeatures:    1,
	}
	ev := TestEvidence{
		Source:      strings.Repeat("x", 100),
		Imports:     []string{"a", "b"},
		Annotations: []string{"@One", "@Two"},
		Features:    []EvidenceFeature{{Name: "one"}, {Name: "two"}},
	}
	got := limits.Apply(ev)
	if len(got.Source) > 16 {
		t.Fatalf("source not bounded: %d", len(got.Source))
	}
	if len(got.Imports) != 1 || len(got.Annotations) != 1 || len(got.Features) != 1 {
		t.Fatalf("lists not capped: %+v", got)
	}
}

func TestLimitsValidateRunRejectsOversizedBatch(t *testing.T) {
	limits := Limits{MaxTestsPerRun: 2}
	err := limits.ValidateRun(make([]TestEvidence, 3))
	if err == nil {
		t.Fatal("expected a limit error")
	}
	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error type = %T, want *LimitError", err)
	}
	if err := limits.ValidateRun(make([]TestEvidence, 2)); err != nil {
		t.Fatalf("batch at the limit was rejected: %v", err)
	}
}

func TestImportFeaturesDedupesAndKeepsOrder(t *testing.T) {
	features := ImportFeatures([]string{
		"testing",
		"github.com/testcontainers/testcontainers-go",
		"net/http",
		"net/http",
	})
	if len(features) != 2 {
		t.Fatalf("features = %+v, want 2", features)
	}
	want := []string{"imports testcontainers", "imports net/http"}
	for i, feature := range features {
		if feature.Name != want[i] {
			t.Fatalf("feature[%d] = %q, want %q", i, feature.Name, want[i])
		}
		if feature.Source != FeatureSourceParser {
			t.Fatalf("feature[%d] source = %q", i, feature.Source)
		}
	}
}

func TestImportSignalMatchesPrefixes(t *testing.T) {
	cases := map[string]string{
		"org.testcontainers.containers.PostgreSQLContainer": "testcontainers",
		"org.junit.jupiter.params.ParameterizedTest":        "junit params",
		"kotlinx.coroutines.runBlocking":                    "kotlinx.coroutines",
		"testing":                                           "",
	}
	for path, want := range cases {
		got, _ := ImportSignal(path)
		if got != want {
			t.Fatalf("ImportSignal(%q) = %q, want %q", path, got, want)
		}
	}
}
