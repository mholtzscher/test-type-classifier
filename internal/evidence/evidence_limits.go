package evidence

import "unicode/utf8"

// Default evidence limits. They are applied by parsers while slicing source and
// again by the classifier before any network call, so an oversized test can
// never leave the machine.
const (
	DefaultMaxSourceBytes     = 8 * 1024
	DefaultMaxImports         = 64
	DefaultMaxAnnotations     = 64
	DefaultMaxFeatures        = 64
	DefaultMaxTestsPerRequest = 1
	DefaultMaxTestsPerRun     = 2000
	// SourceTruncationMarker is appended to source that exceeded a byte limit.
	SourceTruncationMarker = "\n// ... evidence truncated ..."
)

// LimitError reports that evidence exceeded a configured bound before it could
// be sent for classification.
type LimitError struct {
	Subject string
	Limit   int
	Actual  int
}

func (e *LimitError) Error() string {
	return "evidence limit exceeded: " + e.Subject
}

// Limits bounds the size and shape of evidence. A zero field means "use the
// default", so the zero Limits value is usable and safe.
type Limits struct {
	MaxSourceBytes     int
	MaxImports         int
	MaxAnnotations     int
	MaxFeatures        int
	MaxTestsPerRequest int
	MaxTestsPerRun     int
}

// DefaultLimits returns the conservative limits used by the CLI.
func DefaultLimits() Limits {
	return Limits{
		MaxSourceBytes:     DefaultMaxSourceBytes,
		MaxImports:         DefaultMaxImports,
		MaxAnnotations:     DefaultMaxAnnotations,
		MaxFeatures:        DefaultMaxFeatures,
		MaxTestsPerRequest: DefaultMaxTestsPerRequest,
		MaxTestsPerRun:     DefaultMaxTestsPerRun,
	}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxSourceBytes <= 0 {
		l.MaxSourceBytes = d.MaxSourceBytes
	}
	if l.MaxImports <= 0 {
		l.MaxImports = d.MaxImports
	}
	if l.MaxAnnotations <= 0 {
		l.MaxAnnotations = d.MaxAnnotations
	}
	if l.MaxFeatures <= 0 {
		l.MaxFeatures = d.MaxFeatures
	}
	if l.MaxTestsPerRequest <= 0 {
		l.MaxTestsPerRequest = d.MaxTestsPerRequest
	}
	if l.MaxTestsPerRun <= 0 {
		l.MaxTestsPerRun = d.MaxTestsPerRun
	}
	return l
}

// Apply returns a copy of the evidence with source and list fields bounded.
func (l Limits) Apply(ev TestEvidence) TestEvidence {
	l = l.normalized()
	ev.Source = TruncateSource(ev.Source, l.MaxSourceBytes)
	ev.Imports = capStrings(ev.Imports, l.MaxImports)
	ev.Annotations = capStrings(ev.Annotations, l.MaxAnnotations)
	ev.Features = capFeatures(ev.Features, l.MaxFeatures)
	return ev
}

// ValidateRun rejects a batch larger than the configured test-count limit.
func (l Limits) ValidateRun(tests []TestEvidence) error {
	l = l.normalized()
	if len(tests) > l.MaxTestsPerRun {
		return &LimitError{Subject: "tests per run", Limit: l.MaxTestsPerRun, Actual: len(tests)}
	}
	return nil
}

// TruncateSource bounds source to maxBytes, appending SourceTruncationMarker
// when it cuts. The result is always valid UTF-8 and never longer than
// maxBytes (unless maxBytes cannot hold the marker).
func TruncateSource(source string, maxBytes int) string {
	if maxBytes <= 0 || len(source) <= maxBytes {
		return source
	}
	marker := SourceTruncationMarker
	if maxBytes <= len(marker) {
		return cutToRune(source, maxBytes)
	}
	cut := cutToRune(source, maxBytes-len(marker))
	return cut + marker
}

// cutToRune returns at most maxBytes bytes without splitting a UTF-8 rune.
func cutToRune(s string, maxBytes int) string {
	if maxBytes >= len(s) {
		return s
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func capStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func capFeatures(values []EvidenceFeature, limit int) []EvidenceFeature {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
