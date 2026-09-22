# Classifier evaluation

The evaluation dataset is a product artifact, not a generated test fixture. Each JSON Lines record identifies one extracted test and records its repository and language so development and holdout splits can remain repository-disjoint.

## Labeling guide

Choose exactly one primary boundary:

- `unit`: small, in-process scope with external boundaries absent or replaced.
- `integration`: a real database, filesystem, network, process, framework container, or multiple production components.
- `contract`: an independently maintained protocol, schema, compatibility, or consumer-provider agreement.
- `end_to_end`: a complete workflow through a public entry point and real downstream boundaries.
- `performance`: measurement of latency, throughput, allocation, load, or another performance property is the main purpose.
- `smoke`: a shallow startup or availability check.
- `other`: recognizable test, but none of the definitions fit.
- `unknown`: the bounded evidence cannot support a reliable classification.

Traits are independent of the primary boundary. Select only from `property`, `fuzz`, `regression`, `golden`, `snapshot`, `parameterized`, `concurrency`, `database`, `filesystem`, `network`, `containerized`, and `time_sensitive`.

Use the broadest boundary visibly exercised by the extracted body. Do not infer helper behavior that is absent from the evidence. A table-driven test is not automatically a property test. A regression trait requires evidence that the test preserves behavior for a previously observed defect. A test using an in-memory fake is not an integration test. An HTTP call alone is not end-to-end unless it drives a complete workflow through real downstream boundaries.

## Review and adjudication

Two reviewers label the same items independently without seeing predictions. They record `reviewer_a_primary`, `reviewer_b_primary`, and corresponding trait arrays. Disagreements are adjudicated using the rules above and documented in `adjudication_note`; `human_primary` and `human_traits` contain only the adjudicated result.

Before model evaluation, compute Cohen's kappa on the first fixed 30-item calibration sample. Do not treat the sample as accepted until kappa is at least 0.8. Revisions to this guide require a new `label_guide_version` and a fresh agreement check.

## Dataset and splits

`labels.jsonl` contains a 200-item seed manifest split between Go and Kotlin and among repositories. Seed rows marked `pending_human_review: true` are scaffolding and must not be used for threshold selection or release claims. Replace their placeholder references with reviewed, redistributable evidence before running a release evaluation.

Keep repositories disjoint between `development` and `holdout`. Tune question wording and thresholds only on `development`; evaluate the frozen choice once on `holdout`.

Each evaluated row records:

- model, question-set, and extractor versions;
- full primary probability distribution and predicted traits;
- both reviewer labels, adjudication, and final human label;
- language, repository, and split.

Run the evaluator with:

```sh
go run ./evaluation -labels evaluation/labels.jsonl
```

It reports macro F1, per-class precision and recall, per-trait precision and recall, a primary confusion matrix, coverage and accepted accuracy at candidate confidence/margin thresholds, and results by language and repository.

## Threshold decision

Threshold decision `unmeasured-v1`: no confidence or margin threshold is enabled. The CLI therefore marks every result `review`. A future version may replace this decision only after a fixed holdout demonstrates at least 90% precision among automatically accepted primary labels. Record the chosen values, model version, question-set version, extractor version, dataset commit, holdout coverage, and accepted precision here.
