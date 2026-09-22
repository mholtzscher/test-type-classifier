package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// probabilitySumTolerance allows tiny floating-point drift when validating that
// a Choice distribution sums to one.
const probabilitySumTolerance = 0.02

// apiResponse mirrors the documented POST /v1/systemone response body.
type apiResponse struct {
	Model   string               `json:"model"`
	Answers map[string]apiAnswer `json:"answers"`
	Usage   *apiUsage            `json:"usage"`
}

type apiAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
	Noul          *float64           `json:"noul"`
}

type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// mapResponse validates a raw response against the requested question set and
// maps it to a Prediction. Validation is strict: a missing answer, a type
// mismatch, an unknown label, a probability distribution that does not sum to
// one, or an out-of-range confidence or noul is rejected rather than guessed at.
func (c *Client) mapResponse(test evidence.TestEvidence, data []byte) (classification.Prediction, error) {
	var resp apiResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&resp); err != nil {
		return classification.Prediction{}, &ResponseError{Reason: "invalid JSON: " + err.Error()}
	}
	if err := ensureNoTrailingData(decoder); err != nil {
		return classification.Prediction{}, err
	}
	if resp.Model == "" {
		return classification.Prediction{}, &ResponseError{Reason: "missing model"}
	}
	if len(resp.Answers) == 0 {
		return classification.Prediction{}, &ResponseError{Reason: "missing answers"}
	}

	primaryAnswer, ok := resp.Answers[PrimaryQuestionID]
	if !ok {
		return classification.Prediction{}, &ResponseError{Reason: "missing answer for " + PrimaryQuestionID}
	}
	primary, probabilities, confidence, err := validatePrimaryAnswer(primaryAnswer)
	if err != nil {
		return classification.Prediction{}, err
	}

	traits := classification.ParserTraits(test)
	for trait := range c.questions.Traits {
		answer, ok := resp.Answers[TraitQuestionID(trait)]
		if !ok {
			return classification.Prediction{}, &ResponseError{Reason: "missing answer for trait " + string(trait)}
		}
		probability, err := validateNoulAnswer(trait, answer)
		if err != nil {
			return classification.Prediction{}, err
		}
		_, parserPresent := traits[trait]
		present := parserPresent || probability >= classification.TraitPresenceThreshold
		source := classification.TraitSourceJev
		if parserPresent {
			source = classification.TraitSourceCombined
		}
		traits[trait] = classification.TraitPrediction{
			Present:     present,
			Probability: probability,
			Source:      source,
		}
	}

	return classification.Prediction{
		Primary:       primary,
		Probabilities: probabilities,
		Confidence:    confidence,
		Traits:        traits,
		Model:         resp.Model,
	}, nil
}

func ensureNoTrailingData(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return &ResponseError{Reason: "unexpected trailing JSON"}
		}
		return &ResponseError{Reason: "invalid trailing JSON: " + err.Error()}
	}
	return nil
}

func validatePrimaryAnswer(answer apiAnswer) (classification.PrimaryTestType, map[classification.PrimaryTestType]float64, float64, error) {
	if answer.Type != "choice" {
		return "", nil, 0, &ResponseError{Reason: "primary answer type is " + answer.Type + ", want choice"}
	}
	primary, ok := classification.ParsePrimaryTestType(answer.Choice)
	if !ok {
		return "", nil, 0, &ResponseError{Reason: "unknown primary type " + answer.Choice}
	}
	if len(answer.Probabilities) == 0 {
		return "", nil, 0, &ResponseError{Reason: "choice answer has no probabilities"}
	}
	if answer.Confidence == nil {
		return "", nil, 0, &ResponseError{Reason: "choice answer has no confidence"}
	}
	confidence := *answer.Confidence
	if math.IsNaN(confidence) || confidence < 0 || confidence > 1 {
		return "", nil, 0, &ResponseError{Reason: "confidence out of range"}
	}
	probabilities := make(map[classification.PrimaryTestType]float64, len(answer.Probabilities))
	sum := 0.0
	argmax := ""
	argmaxProbability := -1.0
	for label, probability := range answer.Probabilities {
		parsed, ok := classification.ParsePrimaryTestType(label)
		if !ok {
			return "", nil, 0, &ResponseError{Reason: "unknown probability label " + label}
		}
		if math.IsNaN(probability) || probability < 0 || probability > 1 {
			return "", nil, 0, &ResponseError{Reason: "probability out of range for " + label}
		}
		probabilities[parsed] = probability
		sum += probability
		if probability > argmaxProbability {
			argmaxProbability = probability
			argmax = label
		}
	}
	if math.Abs(sum-1) > probabilitySumTolerance {
		return "", nil, 0, &ResponseError{Reason: fmt.Sprintf("probabilities sum to %.4f", sum)}
	}
	if _, ok := answer.Probabilities[answer.Choice]; !ok {
		return "", nil, 0, &ResponseError{Reason: "choice " + answer.Choice + " is not in probabilities"}
	}
	if answer.Choice != argmax {
		return "", nil, 0, &ResponseError{Reason: "choice " + answer.Choice + " is not the highest-probability label"}
	}
	return primary, probabilities, confidence, nil
}

func validateNoulAnswer(trait classification.TestTrait, answer apiAnswer) (float64, error) {
	if answer.Type != "noul" {
		return 0, &ResponseError{Reason: "trait " + string(trait) + " answer type is " + answer.Type + ", want noul"}
	}
	if answer.Noul == nil {
		return 0, &ResponseError{Reason: "trait " + string(trait) + " answer has no noul value"}
	}
	value := *answer.Noul
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, &ResponseError{Reason: "trait " + string(trait) + " noul out of range"}
	}
	return value, nil
}

// IsResponseError reports whether err is a malformed-response error.
func IsResponseError(err error) bool {
	var target *ResponseError
	return errors.As(err, &target)
}
