// Package cli implements the testclassify command: flag parsing, discovery,
// parsing, classification, and report orchestration. Run returns a process exit
// code instead of exiting so the command stays testable; the main package is a
// thin process entry point.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/discovery"
	"github.com/mholtzscher/test-type-classifier/internal/evidence"
	"github.com/mholtzscher/test-type-classifier/internal/jev"
	"github.com/mholtzscher/test-type-classifier/internal/parser/goast"
	"github.com/mholtzscher/test-type-classifier/internal/parser/kotlinast"
	"github.com/mholtzscher/test-type-classifier/internal/report"
)

// Process exit codes returned by Run.
const (
	// ExitSuccess means every discovered file was classified.
	ExitSuccess = 0
	// ExitFailure means a runtime failure such as a parse failure, an
	// authentication error, a malformed response, or a timeout.
	ExitFailure = 1
	// ExitUsage means invalid flags, configuration, or credentials.
	ExitUsage = 2
)

// DefaultMaxFileBytes bounds the source read for one file. The parser slices
// per-test evidence within this bound; the limit here only stops a single
// runaway file from being read into memory.
const DefaultMaxFileBytes = 4 << 20

// SourceLeavesMachineNotice is printed once per run before any test is sent.
const SourceLeavesMachineNotice = "testclassify: extracted test source, imports, annotations, and features are sent to the TypeSafe AI Jev API; test source may contain secrets."

// Run parses args, runs the classification pipeline, and returns a process exit
// code. Credentials are read only from TYPESAFE_API_KEY; TYPESAFE_BASE_URL
// optionally points at a compatible proxy or local fake server.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, os.Getenv, stdout, stderr)
}

// options holds the parsed command configuration.
type options struct {
	model             string
	format            string
	minConfidence     float64
	minMargin         float64
	concurrency       int
	timeout           time.Duration
	include           stringList
	exclude           stringList
	experimentalModel bool
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, paths, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		fmt.Fprintf(stderr, "testclassify: %v\n", err)
		return ExitUsage
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(stderr, "testclassify: %v\n", err)
		return ExitUsage
	}

	apiKey := strings.TrimSpace(getenv("TYPESAFE_API_KEY"))
	if apiKey == "" {
		fmt.Fprintln(stderr, "testclassify: TYPESAFE_API_KEY is not set")
		return ExitUsage
	}

	classifier, err := newClassifier(opts, apiKey, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "testclassify: %v\n", err)
		return ExitUsage
	}

	discoverer := discovery.New(opts.include, opts.exclude)
	files, err := discoverer.Discover(ctx, paths)
	if err != nil {
		fmt.Fprintf(stderr, "testclassify: %v\n", err)
		return ExitFailure
	}

	parsed := collectTests(ctx, files)

	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "testclassify: %v\n", err)
		writeSkippedFiles(stderr, parsed.skipped)
		return ExitFailure
	}

	if len(parsed.tests) == 0 {
		if err := report.NewTableReporter(stdout).WriteReport(ctx, nil); err != nil {
			fmt.Fprintf(stderr, "testclassify: write report: %v\n", err)
			return ExitFailure
		}
		if err := report.WriteSkippedFiles(stderr, parsed.skipped); err != nil {
			fmt.Fprintf(stderr, "testclassify: write skipped files: %v\n", err)
			return ExitFailure
		}
		if len(parsed.skipped) > 0 {
			return ExitFailure
		}
		fmt.Fprintln(stderr, "testclassify: no tests found")
		return ExitSuccess
	}

	fmt.Fprintln(stderr, SourceLeavesMachineNotice)

	predictions, err := classifier.ClassifyTests(ctx, parsed.tests)
	if err != nil {
		fmt.Fprintf(stderr, "testclassify: classification failed: %v\n", err)
		writeSkippedFiles(stderr, parsed.skipped)
		return ExitFailure
	}
	if len(predictions) != len(parsed.tests) {
		fmt.Fprintf(stderr, "testclassify: classifier returned %d predictions for %d tests\n",
			len(predictions), len(parsed.tests))
		return ExitFailure
	}

	policy := opts.policy()
	results := make([]classification.Classification, len(parsed.tests))
	for i := range parsed.tests {
		results[i] = classification.Classification{
			Evidence:   parsed.tests[i],
			Prediction: predictions[i],
			Decision:   policy.DecideClassification(predictions[i]),
		}
	}

	if err := report.NewTableReporter(stdout).WriteReport(ctx, results); err != nil {
		fmt.Fprintf(stderr, "testclassify: write report: %v\n", err)
		return ExitFailure
	}
	if err := report.WriteSkippedFiles(stderr, parsed.skipped); err != nil {
		fmt.Fprintf(stderr, "testclassify: write skipped files: %v\n", err)
		return ExitFailure
	}
	if len(parsed.skipped) > 0 {
		return ExitFailure
	}
	return ExitSuccess
}

func writeSkippedFiles(stderr io.Writer, skipped []report.SkippedFile) {
	if err := report.WriteSkippedFiles(stderr, skipped); err != nil {
		fmt.Fprintf(stderr, "testclassify: write skipped files: %v\n", err)
	}
}

// newClassifier builds the Jev client from the environment.
func newClassifier(opts options, apiKey string, getenv func(string) string) (*jev.Client, error) {
	clientOpts := []jev.Option{
		jev.WithModel(opts.model),
		jev.WithExperimentalModel(opts.experimentalModel),
		jev.WithConcurrency(opts.concurrency),
		jev.WithRequestTimeout(opts.timeout),
	}
	if baseURL := strings.TrimSpace(getenv("TYPESAFE_BASE_URL")); baseURL != "" {
		clientOpts = append(clientOpts, jev.WithBaseURL(baseURL))
	}
	return jev.NewClient(apiKey, clientOpts...)
}

// parseArgs parses flags interspersed with positional paths.
func parseArgs(args []string, stderr io.Writer) (options, []string, error) {
	var opts options
	flags := flag.NewFlagSet("testclassify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.model, "model", jev.DefaultModel, "versioned Jev model")
	flags.StringVar(&opts.format, "format", "table", "output format; only table is supported")
	flags.Float64Var(&opts.minConfidence, "min-confidence", 0, "minimum confidence required for acceptance")
	flags.Float64Var(&opts.minMargin, "min-margin", 0, "minimum gap between the top two primary probabilities required for acceptance")
	flags.IntVar(&opts.concurrency, "concurrency", jev.DefaultConcurrency, "maximum in-flight Jev requests")
	flags.DurationVar(&opts.timeout, "timeout", jev.DefaultRequestTimeout, "timeout for each Jev request")
	flags.Var(&opts.include, "include", "additional file pattern (repeatable)")
	flags.Var(&opts.exclude, "exclude", "excluded path pattern (repeatable)")
	flags.BoolVar(&opts.experimentalModel, "experimental-model", false, "permit a floating Jev model alias")

	var paths []string
	remaining := args
	for len(remaining) > 0 {
		if err := flags.Parse(remaining); err != nil {
			return opts, nil, err
		}
		remaining = flags.Args()
		if len(remaining) == 0 {
			break
		}
		paths = append(paths, remaining[0])
		remaining = remaining[1:]
	}
	if opts.format != "table" {
		return opts, nil, fmt.Errorf("unsupported format %q: only \"table\" is supported", opts.format)
	}
	return opts, paths, nil
}

func (o options) validate() error {
	if o.concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if o.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if err := validateThreshold("min-confidence", o.minConfidence); err != nil {
		return err
	}
	if err := validateThreshold("min-margin", o.minMargin); err != nil {
		return err
	}
	if (o.minConfidence > 0) != (o.minMargin > 0) {
		return fmt.Errorf("min-confidence and min-margin must both be set to enable acceptance")
	}
	return nil
}

func validateThreshold(name string, value float64) error {
	if math.IsNaN(value) || value < 0 || value > 1 {
		return fmt.Errorf("%s must be between 0 and 1", name)
	}
	return nil
}

// policy enables acceptance only when both measured thresholds are set. With no
// thresholds every prediction is routed to review.
func (o options) policy() classification.ClassificationPolicy {
	if o.minConfidence > 0 && o.minMargin > 0 {
		return classification.NewThresholdPolicy(o.minConfidence, o.minMargin)
	}
	return classification.DefaultPolicy()
}

// parsedTests is the result of reading and parsing every discovered file.
type parsedTests struct {
	tests   []evidence.TestEvidence
	skipped []report.SkippedFile
}

// collectTests parses each discovered file with its language parser. A read or
// parse failure is recorded and skipped so the rest of the run continues.
func collectTests(ctx context.Context, files []discovery.File) parsedTests {
	parsers := map[evidence.Language]evidence.TestParser{
		evidence.LanguageGo:     goast.NewParser(),
		evidence.LanguageKotlin: kotlinast.NewParser(),
	}
	var result parsedTests
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			break
		}
		parser, ok := parsers[file.Language]
		if !ok {
			result.skipped = append(result.skipped, report.SkippedFile{
				Path:   file.Path,
				Reason: "unsupported language " + string(file.Language),
			})
			continue
		}
		source, err := readSource(file.SourcePath)
		if err != nil {
			result.skipped = append(result.skipped, report.SkippedFile{Path: file.Path, Reason: err.Error()})
			continue
		}
		tests, err := parser.ParseTests(ctx, file.Path, source)
		if err != nil {
			result.skipped = append(result.skipped, report.SkippedFile{Path: file.Path, Reason: err.Error()})
			continue
		}
		result.tests = append(result.tests, tests...)
	}
	return result
}

// readSource reads a file up to DefaultMaxFileBytes and reports a limit error
// instead of reading an unbounded file into memory.
func readSource(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, DefaultMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(data) > DefaultMaxFileBytes {
		return nil, fmt.Errorf("file exceeds %d byte limit", DefaultMaxFileBytes)
	}
	return data, nil
}
