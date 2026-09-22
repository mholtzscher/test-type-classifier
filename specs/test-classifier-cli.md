# Test classifier CLI feature plan

## Problem

Build a Go CLI that finds Go and Kotlin tests, extracts bounded evidence from each test, and asks TypeSafe AI's Jev model to classify the test. The first release prints a report. It does not modify source files or enforce CI policy.

The tool must distinguish the test's execution boundary from independent traits. A database-backed regression test, for example, has primary type `integration` and traits `database` and `regression`.

## Scope

### Included

- Discover Go and Kotlin test files under one or more paths.
- Parse syntax without resolving dependencies or compiler symbols.
- Extract one record per test, including nested Go subtests when practical.
- Send only the extracted test, imports, annotations, and deterministic syntax features to Jev.
- Assign one primary type and zero or more traits.
- Print a stable table with confidence and review status.
- Evaluate the classifier against a human-labeled dataset.

### Not included

- Source changes, annotations, file moves, or CI failure policy.
- Full Go or Kotlin type resolution.
- Sending helpers, production code, or complete files to the hosted API.
- Training or fine-tuning a model.
- Inferring whether a test is effective or well written.

## Classification contract

### Primary type

Each test gets exactly one value:

| Type | Definition |
| --- | --- |
| `unit` | Exercises a small in-process scope with external boundaries replaced or absent. |
| `integration` | Exercises a real boundary such as a database, filesystem, network service, process, framework container, or multiple production components. |
| `contract` | Verifies an independently maintained consumer-provider, protocol, schema, or compatibility contract. |
| `end_to_end` | Drives a complete user or system workflow through a public entry point and one or more real downstream boundaries. |
| `performance` | Measures latency, throughput, allocation, load, or another performance property as its main purpose. |
| `smoke` | Performs a shallow availability or startup check rather than detailed behavioral verification. |
| `other` | The test is recognizable, but none of the definitions fit. |
| `unknown` | The available evidence does not support a reliable classification. |

Boundary type and test technique are deliberately separate. `property`, `fuzz`, and `regression` are traits, not alternatives to `unit` or `integration`.

### Traits

The first taxonomy is closed and versioned:

`property`, `fuzz`, `regression`, `golden`, `snapshot`, `parameterized`, `concurrency`, `database`, `filesystem`, `network`, `containerized`, `time_sensitive`.

Deterministic parser rules should set unambiguous traits such as Go fuzz functions, Go benchmarks, Kotlin parameterized-test annotations, and known snapshot calls. Jev handles semantic judgments and ambiguous signals. Code combines both sources and retains their provenance.

## Recommended design

Use a local parsing pipeline and a small direct HTTP client for `POST /v1/systemone`. TypeSafe documents official Python and JavaScript SDKs, but the HTTP API is simple and avoids embedding another runtime in this Go CLI.

For every test, send a structured state containing the language, test name, annotations, imports, source body, and parser-derived features. Ask one `Choice` question for the primary type and one `Noul` question for each non-deterministic trait in the same request. Jev evaluates the questions in parallel.

Use a versioned Jev model for repeatable evaluation and releases. Allow `jev-latest` only through an explicit experimental option. Do not hard-code an acceptance threshold before measuring the labeled dataset. Until then, the CLI should default every result to `review` while still displaying the winning label and probabilities.

### Data flow

```text
paths -> file discovery -> language parser -> TestEvidence
      -> deterministic traits -> Jev questions -> Classification
      -> confidence policy -> table report
```

## Implementation shape

### Core types

```go
type Language string

const (
	LanguageGo     Language = "go"
	LanguageKotlin Language = "kotlin"
)

type TestID struct {
	Path      string
	Qualified string
}

type TestEvidence struct {
	ID          TestID
	Language    Language
	Name        string
	Annotations []string
	Imports     []string
	Source      string
	Features    []EvidenceFeature
}

type EvidenceFeature struct {
	Name   string
	Source string
}

type PrimaryTestType string
type TestTrait string

type Prediction struct {
	Primary       PrimaryTestType
	Probabilities map[PrimaryTestType]float64
	Confidence    float64
	Traits        map[TestTrait]TraitPrediction
	Model         string
}

type TraitPrediction struct {
	Present     bool
	Probability float64
	Source      string // parser, jev, or combined
}

type ReviewDecision string

const (
	ReviewDecisionAccept ReviewDecision = "accept"
	ReviewDecisionReview ReviewDecision = "review"
)

type Classification struct {
	Evidence   TestEvidence
	Prediction Prediction
	Decision   ReviewDecision
}
```

### Interfaces

```go
// TestParser extracts individual tests and syntax evidence without resolving symbols.
type TestParser interface {
	ParseTests(ctx context.Context, path string, source []byte) ([]TestEvidence, error)
}

// TestClassifier predicts one boundary type and independent traits for test evidence.
type TestClassifier interface {
	ClassifyTests(ctx context.Context, tests []TestEvidence) ([]Prediction, error)
}

// ClassificationPolicy converts model uncertainty into accept or review decisions.
type ClassificationPolicy interface {
	DecideClassification(prediction Prediction) ReviewDecision
}

// ClassificationReporter writes classifications without changing source files.
type ClassificationReporter interface {
	WriteReport(ctx context.Context, results []Classification) error
}
```

`TestClassifier` accepts a slice so the Jev client can control concurrency and request limits. Each test remains a separate Jev state because questions in one request share one state.

### Jev request

```json
{
  "model": "jev-1.13.0",
  "state": {
    "language": "go",
    "test_name": "TestCreateOrder/database_failure",
    "annotations": [],
    "imports": ["testing", "github.com/testcontainers/testcontainers-go"],
    "features": ["calls t.Run", "imports testcontainers"],
    "source": "func TestCreateOrder(t *testing.T) { ... }"
  },
  "questions": {
    "primary_type": {
      "type": "choice",
      "instructions": "Classify the test by the broadest real execution boundary it exercises. Classify technique separately from boundary.",
      "criteria": {
        "unit": "Small in-process scope; external boundaries are absent or replaced.",
        "integration": "Uses a real external boundary or joins multiple production components.",
        "contract": "Checks an independently maintained protocol, schema, or consumer-provider agreement.",
        "end_to_end": "Drives a complete workflow through a public entry point and real downstream boundaries.",
        "performance": "Its main purpose is measuring a performance property.",
        "smoke": "Shallow startup or availability check.",
        "other": "A test whose main type is not covered above.",
        "unknown": "The supplied evidence is insufficient."
      }
    },
    "trait_regression": {
      "type": "noul",
      "instructions": "Is this test specifically preserving behavior for a previously observed defect?"
    }
  }
}
```

The production question set must include every non-deterministic trait and structured `what`, `not_for`, and `examples` descriptions where labeled errors show confusion.

## Project layout

```text
cmd/testclassify/
  main.go                         new, process entry point only
internal/cli/
  classify_command.go             new, flags and command orchestration
internal/discovery/
  test_file_discovery.go          new, path walking and language detection
internal/evidence/
  test_evidence.go                new, shared evidence types
  evidence_limits.go              new, source and request size limits
internal/parser/goast/
  go_test_parser.go               new, go/parser extraction and subtests
internal/parser/kotlinast/
  kotlin_test_parser.go           new, Tree-sitter Kotlin extraction
internal/classification/
  test_taxonomy.go                new, primary types and trait definitions
  classification_policy.go       new, confidence and margin policy
internal/jev/
  jev_http_client.go              new, authenticated API and retry handling
  jev_question_set.go             new, versioned questions and criteria
  jev_response_mapping.go         new, API response validation and mapping
internal/report/
  classification_table.go        new, deterministic terminal output
evaluation/
  labels.jsonl                    new, reviewed examples with provenance
  classifier_evaluation.go       new, confusion matrix and threshold metrics
  README.md                       new, labeling rules and adjudication process
```

The Kotlin parser should sit behind `TestParser`. Tree-sitter is a practical first choice, but its Go bindings may add CGO and cross-compilation costs. Validate that packaging constraint with a small spike before committing to a library. If static binaries are required, compare a pure-Go parser or a bundled parser process before implementation.

## CLI contract

```text
testclassify [paths...] [flags]

--model string          versioned Jev model
--format table          table in v1; reserve json for later
--min-confidence float  policy threshold selected from evaluation
--min-margin float      minimum gap between the top two primary probabilities
--concurrency int       maximum in-flight Jev requests
--timeout duration      per-request timeout
--include pattern       additional file pattern
--exclude pattern       excluded path pattern
--experimental-model    permit floating model aliases
```

Credentials come only from `TYPESAFE_API_KEY`. Optional `TYPESAFE_BASE_URL` supports testing and compatible proxies. The tool must never print the key or include it in error text.

Example report:

```text
TYPE         TRAITS                 CONF  STATUS  TEST
unit         parameterized          0.94  accept  ./cart_test.go::TestTotals
integration database,containerized  0.88  accept  ./OrderTest.kt::creates order
unknown                             0.31  review  ./legacy_test.go::TestThing
```

## Evaluation plan

Start with at least 200 tests, split across Go and Kotlin and across repositories. Sample difficult neighboring categories on purpose. Two reviewers should label each item using the written taxonomy and adjudicate disagreements.

Keep a fixed holdout set. Tune question wording and thresholds only on the development set. Report:

- Macro F1 and per-class precision and recall for primary type.
- Per-trait precision and recall.
- Confusion matrix, especially `unit` versus `integration` and `integration` versus `end_to_end`.
- Coverage at the chosen confidence and margin thresholds.
- Accuracy among automatically accepted predictions.
- Results by language and repository to expose dataset leakage.

Record the model version, question-set version, source extractor version, full probability distribution, and human label. A target for the first useful release is at least 90% precision among accepted primary labels while routing uncertain cases to review. This is an acceptance target, not an assumed property of Jev.

## Error and privacy behavior

- Retry `429` and `529` responses with exponential backoff and bounded jitter.
- Return a clear nonzero exit code for authentication, malformed response, timeout, and parse failures.
- Continue past individual parse failures unless `--strict` is later added. Report skipped files at the end.
- Apply byte and test-count limits before network calls.
- Print a one-time notice that extracted source leaves the machine.
- Do not persist source or API responses by default.
- Redact string literals marked by a future secret scanner only after evaluation proves redaction does not remove needed evidence. For v1, document that source may contain secrets and require explicit user invocation.

## Deliverables

1. **D1, taxonomy and labeled seed set, size L.** Define the annotation guide and label at least 200 representative tests.
   - Owning paths: `internal/classification/test_taxonomy.go`, `evaluation/labels.jsonl`, `evaluation/README.md`
   - Acceptance: two reviewers can independently label a 30-item sample with Cohen's kappa of at least 0.8 after adjudication guidance is finalized.

2. **D2, parser spike, size M.** Prove extraction for Go tests, Go subtests, Kotlin JUnit tests, and Kotlin parameterized tests.
   - Owning paths: `internal/parser/goast/`, `internal/parser/kotlinast/`, `internal/evidence/`
   - Depends on: D1
   - Acceptance: golden fixtures recover test identity, annotations, imports, body, and deterministic features without sending whole files.

3. **D3, Jev classifier, size M.** Implement the HTTP contract, question set, response validation, retries, and fake-server tests.
   - Owning paths: `internal/jev/`, `internal/classification/classification_policy.go`
   - Depends on: D1
   - Acceptance: contract tests cover success, malformed responses, authentication failure, rate limits, overload, timeout, and model-version recording.

4. **D4, CLI and report, size M.** Connect discovery, parsing, classification, and deterministic table output.
   - Owning paths: `cmd/testclassify/`, `internal/cli/`, `internal/discovery/`, `internal/report/`
   - Depends on: D2, D3
   - Acceptance: a fixture repository containing both languages produces one stable row per discovered test and never transmits excluded file content.

5. **D5, evaluation and threshold selection, size L.** Run the fixed dataset, inspect errors, revise the question descriptions on the development split, and select confidence and margin thresholds.
   - Owning paths: `evaluation/classifier_evaluation.go`, `evaluation/README.md`
   - Depends on: D3, D4
   - Acceptance: the report includes all listed metrics and records a versioned threshold decision. Release only if accepted predictions meet the precision target on holdout data.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| The taxonomy mixes boundaries and techniques. | Keep one boundary label and independent traits; adjudicate ambiguous examples in the annotation guide. |
| Extracted test bodies omit helper behavior needed to classify a boundary. | Emit `unknown`, measure this failure mode, then consider bounded helper summaries in a later version rather than silently sending more code. |
| Kotlin parsing makes static Go binaries difficult. | Complete the parser and packaging spike before building the rest of the Kotlin path. |
| A floating model changes accuracy or confidence calibration. | Pin a version for evaluation and releases, and log the model returned by the API. |
| Confidence is mistaken for correctness. | Select thresholds on held-out labeled data and display review status separately from confidence. |
| Repository conventions leak into the dataset. | Split evaluation by repository and keep repositories disjoint where possible. |

## Recommendation

Build a thin vertical slice first: one Go parser, one Kotlin parser, the versioned taxonomy, direct Jev HTTP calls, and a table report. Treat the curated dataset as part of the product, not a final validation task. The classifier cannot be made reliable by parser sophistication alone because labels such as `integration` and `end_to_end` depend on a written boundary definition and measured behavior on real tests.

## Sources

- TypeSafe API reference: <https://docs.typesafe.ai/api>
- TypeSafe Choice guidance: <https://docs.typesafe.ai/primitives/choice>
- TypeSafe documentation index: <https://docs.typesafe.ai/llms.txt>
