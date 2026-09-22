package evidence

import "strings"

// importRule maps an import path to the evidence feature that reports the
// boundary-signaling dependency. Exact rules match the whole path; prefix rules
// match a package subtree.
type importRule struct {
	match  string
	prefix bool
	label  string
}

// importRules recognizes dependencies that reliably hint at a boundary. Import
// signals are features only: the classifier decides whether the test actually
// crosses the boundary, because an import can appear without being exercised.
var importRules = []importRule{
	{match: "github.com/testcontainers/testcontainers-go", label: "testcontainers"},
	{match: "github.com/stretchr/testify", label: "testify"},
	{match: "github.com/stretchr/testify/assert", label: "testify"},
	{match: "github.com/stretchr/testify/require", label: "testify"},
	{match: "database/sql", label: "database/sql"},
	{match: "net/http/httptest", label: "httptest"},
	{match: "net/http", label: "net/http"},
	{match: "os/exec", label: "os/exec"},
	{match: "os", label: "os"},
	{match: "time", label: "time"},
	{match: "sync", label: "sync"},

	{match: "org.testcontainers", prefix: true, label: "testcontainers"},
	{match: "org.junit.jupiter.params", prefix: true, label: "junit params"},
	{match: "org.junit.jupiter", prefix: true, label: "junit jupiter"},
	{match: "org.springframework", prefix: true, label: "spring"},
	{match: "java.sql", prefix: true, label: "java.sql"},
	{match: "javax.sql", prefix: true, label: "javax.sql"},
	{match: "kotlinx.coroutines", prefix: true, label: "kotlinx.coroutines"},
	{match: "io.mockk", prefix: true, label: "mockk"},
	{match: "io.kotest", prefix: true, label: "kotest"},
	{match: "kotlin.test", prefix: true, label: "kotlin.test"},
}

// ImportSignal returns the feature label that reports a boundary-signaling
// import path, and whether the path is recognized.
func ImportSignal(path string) (string, bool) {
	for _, rule := range importRules {
		if rule.prefix {
			if path == rule.match || strings.HasPrefix(path, rule.match+".") {
				return rule.label, true
			}
			continue
		}
		if path == rule.match {
			return rule.label, true
		}
	}
	return "", false
}

// ImportFeatures converts an ordered import list into deduplicated, ordered
// parser features for the recognized dependencies.
func ImportFeatures(imports []string) []EvidenceFeature {
	var features []EvidenceFeature
	seen := make(map[string]bool, len(imports))
	for _, path := range imports {
		label, ok := ImportSignal(path)
		if !ok || seen[label] {
			continue
		}
		seen[label] = true
		features = append(features, EvidenceFeature{
			Name:   "imports " + label,
			Source: FeatureSourceParser,
		})
	}
	return features
}
