# testclassify

`testclassify` discovers Go and Kotlin tests, extracts bounded evidence from each test, and asks TypeSafe AI's Jev model to classify the test's execution boundary and independent traits. It prints a report; it never rewrites source files or enforces CI policy.

## Privacy

Running the command is an explicit request to send each extracted test body, its imports, annotations, and syntax features to TypeSafe AI. Test source can contain secrets. This version does not redact string literals, send helper or production code, persist source, or persist API responses. Review the source being classified before invoking the command.

## Build and run

```sh
go build -o bin/testclassify ./cmd/testclassify
export TYPESAFE_API_KEY="$(cat /path/to/api-key)"
bin/testclassify ./path/to/repository
```

Credentials are read only from `TYPESAFE_API_KEY`. Set `TYPESAFE_BASE_URL` only for a compatible proxy or local fake server.

```text
testclassify [paths...] [flags]

--model string          pinned Jev model (default "jev-1.13.0")
--format table          output format; only table is supported
--min-confidence float  measured confidence threshold; both thresholds are required to enable acceptance
--min-margin float      measured gap between the top two primary probabilities
--concurrency int       maximum in-flight Jev requests
--timeout duration      timeout for each Jev request
--include pattern       additional path pattern (repeatable)
--exclude pattern       excluded path pattern (repeatable)
--experimental-model    permit a floating model alias such as jev-latest
```

With no measured thresholds, every result is marked `review`, regardless of model confidence. See [`evaluation/README.md`](evaluation/README.md) for labeling, evaluation, and threshold selection.

## Parser packaging decision

Go uses the standard library syntax parser. The Kotlin parser is isolated behind the same parser interface and uses a bounded, dependency-free syntax extractor in the first release. This avoids CGO and keeps cross-compilation straightforward. Tree-sitter Kotlin is the preferred future replacement if fixture and evaluation results show that the lightweight extractor misses real syntax; isolating the parser makes that change local.
