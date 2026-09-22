package jev

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
)

func TestDefaultQuestionSetVersionIsSet(t *testing.T) {
	questions := DefaultQuestionSet()
	if questions.Version == "" {
		t.Fatal("question set version is empty")
	}
	if questions.Version != QuestionSetVersion {
		t.Fatalf("version = %q, want %q", questions.Version, QuestionSetVersion)
	}
}

func TestDefaultQuestionSetAsksEveryNonDeterministicTrait(t *testing.T) {
	questions := DefaultQuestionSet()
	for _, trait := range classification.NonDeterministicTraits() {
		question, ok := questions.Traits[trait]
		if !ok {
			t.Fatalf("missing question for trait %q", trait)
		}
		if question.Type != "noul" {
			t.Fatalf("trait %q question type = %q, want noul", trait, question.Type)
		}
		if question.Instructions == nil {
			t.Fatalf("trait %q has no instructions", trait)
		}
	}
	for _, trait := range classification.DeterministicTraits() {
		if _, ok := questions.Traits[trait]; ok {
			t.Fatalf("deterministic trait %q should not have a question", trait)
		}
	}
	if questions.Primary.Type != "choice" {
		t.Fatalf("primary question type = %q, want choice", questions.Primary.Type)
	}
}

func TestDefaultQuestionSetPrimaryCriteriaCoversTaxonomy(t *testing.T) {
	questions := DefaultQuestionSet()
	for _, primary := range classification.PrimaryTestTypes() {
		if _, ok := questions.Primary.Criteria[string(primary)]; !ok {
			t.Fatalf("primary criteria missing %q", primary)
		}
	}
}

func TestQuestionSetMarshalsWithStructuredDescriptions(t *testing.T) {
	requests := DefaultQuestionSet().Requests()
	body, err := json.Marshal(requests)
	if err != nil {
		t.Fatalf("marshal questions: %v", err)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal questions: %v", err)
	}
	primary := decoded[PrimaryQuestionID]
	criteria, ok := primary["criteria"].(map[string]any)
	if !ok {
		t.Fatalf("primary criteria = %#v", primary["criteria"])
	}
	unit, ok := criteria["unit"].(map[string]any)
	if !ok {
		t.Fatalf("unit criteria = %#v, want a structured object", criteria["unit"])
	}
	if _, ok := unit["what"]; !ok {
		t.Fatalf("unit criteria missing what: %#v", unit)
	}

	regression := decoded[TraitQuestionID(classification.TraitRegression)]
	instructions, ok := regression["instructions"].(map[string]any)
	if !ok {
		t.Fatalf("regression instructions = %#v, want a structured object", regression["instructions"])
	}
	for _, field := range []string{"question", "what", "not_for", "examples"} {
		if _, ok := instructions[field]; !ok {
			t.Fatalf("regression instructions missing %q: %#v", field, instructions)
		}
	}
}
