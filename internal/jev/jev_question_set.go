package jev

import "github.com/mholtzscher/test-type-classifier/internal/classification"

// DefaultModel is the pinned, versioned Jev model used for repeatable evaluation
// and releases. Floating aliases like jev-latest are rejected unless the
// experimental model option is set.
const DefaultModel = "jev-1.13.0"

// QuestionSetVersion identifies the question wording and criteria. Evaluation
// records it alongside the model version so a prediction can be reproduced.
const QuestionSetVersion = "1.0.0"

// PrimaryQuestionID is the question id for the single Choice primary type.
const PrimaryQuestionID = "primary_type"

// Question is one typed TypeSafe question. Instructions and criteria values are
// any because the API accepts strings, objects, and arrays.
type Question struct {
	Type         string         `json:"type"`
	Instructions any            `json:"instructions"`
	Criteria     map[string]any `json:"criteria,omitempty"`
}

// QuestionSet is the versioned set of questions asked for every test.
type QuestionSet struct {
	Version string
	Primary Question
	Traits  map[classification.TestTrait]Question
}

// TraitQuestionID returns the request key for a trait Noul question.
func TraitQuestionID(trait classification.TestTrait) string {
	return "trait_" + string(trait)
}

// Requests returns the full question map sent in one Jev request: the primary
// Choice plus one Noul per non-deterministic trait.
func (qs QuestionSet) Requests() map[string]Question {
	questions := make(map[string]Question, 1+len(qs.Traits))
	questions[PrimaryQuestionID] = qs.Primary
	for trait, question := range qs.Traits {
		questions[TraitQuestionID(trait)] = question
	}
	return questions
}

// defaultTraitQuestions holds the Noul wording for every non-deterministic
// trait. Deterministic traits are set by parser rules and are not asked.
var defaultTraitQuestions = map[classification.TestTrait]Question{
	classification.TraitProperty: {
		Type:         "noul",
		Instructions: "Does this test assert a general invariant or property across many generated or varied inputs rather than checking one fixed example?",
		Criteria: map[string]any{
			"true":  "A property, invariant, or law is checked over generated or varied inputs.",
			"false": "Only specific, hand-written examples are checked.",
		},
	},
	classification.TraitRegression: {
		Type: "noul",
		Instructions: map[string]any{
			"question": "Is this test specifically preserving behavior for a previously observed defect?",
			"what":     "A regression test is tied to a bug that already happened.",
			"not_for":  "Ordinary new-feature verification and generic edge-case coverage.",
			"examples": []string{
				"TestCreateOrder_HandlesNullCustomer reproduces issue 4512.",
				"Verifies the fix for the duplicate-charge report.",
			},
		},
		Criteria: map[string]any{
			"true":  "The test references or clearly encodes a specific past defect or fix.",
			"false": "No specific past defect is referenced or implied.",
		},
	},
	classification.TraitDatabase: {
		Type: "noul",
		Instructions: map[string]any{
			"question": "Does this test exercise a real database rather than an in-memory replacement or absent database?",
			"what":     "A real database engine, its driver, or its schema is contacted.",
			"not_for":  "In-memory fakes, repositories stubbed in process.",
			"examples": []string{
				"Opens a Postgres connection and asserts a committed row.",
				"Runs the migration and queries the real schema.",
			},
		},
		Criteria: map[string]any{
			"true":  "A real database boundary is exercised.",
			"false": "No real database boundary is exercised.",
		},
	},
	classification.TraitFilesystem: {
		Type: "noul",
		Instructions: map[string]any{
			"question": "Does this test exercise the real filesystem?",
			"what":     "Reads from or writes to real files or directories outside the process's compiled assets.",
			"not_for":  "Embedded fixtures that never touch disk, in-memory file abstractions.",
			"examples": []string{
				"Writes a temporary file and reads it back.",
				"Asserts permissions on a created directory.",
			},
		},
		Criteria: map[string]any{
			"true":  "The real filesystem is exercised.",
			"false": "No real filesystem boundary is exercised.",
		},
	},
	classification.TraitNetwork: {
		Type: "noul",
		Instructions: map[string]any{
			"question": "Does this test exercise a real network boundary?",
			"what":     "Talks to a real server, socket, DNS name, or HTTP peer.",
			"not_for":  "In-process HTTP test servers, recorded fixtures, stubbed clients.",
			"examples": []string{
				"Calls a deployed service over HTTP.",
				"Opens a socket to a broker.",
			},
		},
		Criteria: map[string]any{
			"true":  "A real network boundary is exercised.",
			"false": "No real network boundary is exercised.",
		},
	},
	classification.TraitContainerized: {
		Type: "noul",
		Instructions: map[string]any{
			"question": "Does this test start or depend on a container or orchestrated workload?",
			"what":     "Starts containers, pods, or another container runtime as part of the test.",
			"not_for":  "Local processes, in-process servers, and plain database connections.",
			"examples": []string{
				"Starts a testcontainers Postgres container.",
				"Applies manifests to a Kubernetes test cluster.",
			},
		},
		Criteria: map[string]any{
			"true":  "A container or orchestrated workload is part of the test.",
			"false": "No container or orchestrated workload is involved.",
		},
	},
	classification.TraitTimeSensitive: {
		Type:         "noul",
		Instructions: "Does this test depend on wall-clock time, delays, sleeps, or scheduling order in a way that makes it time-sensitive?",
		Criteria: map[string]any{
			"true":  "Timing, delays, or clock behavior decide the outcome.",
			"false": "The outcome does not depend on timing.",
		},
	},
}

// DefaultQuestionSet returns the production question set for the closed version
// 1 taxonomy.
func DefaultQuestionSet() QuestionSet {
	primary := Question{
		Type:         "choice",
		Instructions: "Classify the test by the broadest real execution boundary it exercises. Classify technique separately from boundary.",
		Criteria: map[string]any{
			"unit": primaryCriteria(
				"Exercises a small in-process scope with external boundaries replaced or absent.",
				[]string{"Talking to a real database, filesystem, network peer, or process."},
				[]string{"Parses an in-memory string and asserts the resulting struct."},
			),
			"integration": primaryCriteria(
				"Exercises a real boundary such as a database, filesystem, network service, process, framework container, or joins multiple production components.",
				[]string{"A single pure function with no real boundary."},
				[]string{"Writes and reads a real Postgres table.", "Wires a repository to a real database."},
			),
			"contract": primaryCriteria(
				"Verifies an independently maintained consumer-provider, protocol, schema, or compatibility contract.",
				[]string{"Internal consistency checks that no external party depends on."},
				[]string{"Consumer-driven contract test against a provider's published pact."},
			),
			"end_to_end": primaryCriteria(
				"Drives a complete user or system workflow through a public entry point and one or more real downstream boundaries.",
				[]string{"A single boundary tested in isolation."},
				[]string{"Places an order through the public API and asserts fulfillment downstream."},
			),
			"performance": primaryCriteria(
				"Measures latency, throughput, allocation, load, or another performance property as its main purpose.",
				[]string{"Correctness assertions that merely happen to be fast."},
				[]string{"A benchmark measuring allocations per operation."},
			),
			"smoke": primaryCriteria(
				"Performs a shallow availability or startup check rather than detailed behavioral verification.",
				[]string{"Detailed behavioral verification of a feature."},
				[]string{"Asserts the service starts and answers a health probe."},
			),
			"other": primaryCriteria(
				"A recognizable test whose main type is not covered above.",
				nil,
				nil,
			),
			"unknown": primaryCriteria(
				"The supplied evidence is insufficient to choose a reliable type.",
				nil,
				nil,
			),
		},
	}
	traits := make(map[classification.TestTrait]Question, len(classification.NonDeterministicTraits()))
	for _, trait := range classification.NonDeterministicTraits() {
		question, ok := defaultTraitQuestions[trait]
		if !ok {
			continue
		}
		traits[trait] = question
	}
	return QuestionSet{
		Version: QuestionSetVersion,
		Primary: primary,
		Traits:  traits,
	}
}

func primaryCriteria(what string, notFor, examples []string) map[string]any {
	description := map[string]any{"what": what}
	if len(notFor) > 0 {
		description["not_for"] = notFor
	}
	if len(examples) > 0 {
		description["examples"] = examples
	}
	return description
}
