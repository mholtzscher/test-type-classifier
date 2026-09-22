package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

const testAPIKey = "test-key"

func testEvidence() evidence.TestEvidence {
	return evidence.TestEvidence{
		ID:          evidence.TestID{Path: "./order_test.go", Qualified: "TestCreateOrder"},
		Language:    evidence.LanguageGo,
		Name:        "TestCreateOrder",
		Annotations: []string{},
		Imports:     []string{"testing", "github.com/testcontainers/testcontainers-go"},
		Source:      "func TestCreateOrder(t *testing.T) {}",
		Features: []evidence.EvidenceFeature{
			{Name: evidence.FeatureGoTestFunction, Source: evidence.FeatureSourceParser},
		},
	}
}

// responseBody builds a documented-shape System One response with every
// non-deterministic trait answered.
func responseBody(t *testing.T, model, primary string, confidence, noul float64) []byte {
	t.Helper()
	types := classification.PrimaryTestTypes()
	probabilities := make(map[string]float64, len(types))
	remaining := 1.0 - 0.7
	share := remaining / float64(len(types)-1)
	for _, primaryType := range types {
		if string(primaryType) == primary {
			probabilities[string(primaryType)] = 0.7
			continue
		}
		probabilities[string(primaryType)] = share
	}
	answers := map[string]any{
		PrimaryQuestionID: map[string]any{
			"type":          "choice",
			"choice":        primary,
			"probabilities": probabilities,
			"confidence":    confidence,
		},
	}
	for _, trait := range classification.NonDeterministicTraits() {
		answers[TraitQuestionID(trait)] = map[string]any{"type": "noul", "noul": noul}
	}
	body, err := json.Marshal(map[string]any{
		"model":   model,
		"answers": answers,
		"usage":   map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return body
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return decoded
}

func encodeBody(t *testing.T, decoded map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return body
}

// newTestClient builds a client against a fake server with deterministic
// backoff by default.
func newTestClient(t *testing.T, server *httptest.Server, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
		withSleep(func(context.Context, time.Duration) error { return nil }),
		withJitter(func(delay time.Duration) time.Duration { return delay }),
	}
	client, err := NewClient(testAPIKey, append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func writeJSON(w http.ResponseWriter, body []byte, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func TestClassifyTestsSuccessRecordsResponseModel(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryIntegration), 0.88, 0.9), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	predictions, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if len(predictions) != 1 {
		t.Fatalf("got %d predictions", len(predictions))
	}
	prediction := predictions[0]
	if prediction.Primary != classification.PrimaryIntegration {
		t.Fatalf("primary = %q", prediction.Primary)
	}
	if prediction.Confidence != 0.88 {
		t.Fatalf("confidence = %v", prediction.Confidence)
	}
	if prediction.Model != "jev-1.13.0" {
		t.Fatalf("model = %q, want recorded response version", prediction.Model)
	}
	if prediction.Probabilities[classification.PrimaryIntegration] != 0.7 {
		t.Fatalf("probabilities = %+v", prediction.Probabilities)
	}
	if got := prediction.Traits[classification.TraitRegression]; !got.Present || got.Source != classification.TraitSourceJev {
		t.Fatalf("regression trait = %+v", got)
	}
	if captured == nil {
		t.Fatal("server did not receive a request body")
	}
}

func TestClassifyTestsSendsVersionedQuestionSetAndState(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	if _, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()}); err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if captured["model"] != DefaultModel {
		t.Fatalf("request model = %v, want %q", captured["model"], DefaultModel)
	}
	state, ok := captured["state"].(map[string]any)
	if !ok {
		t.Fatalf("state = %#v", captured["state"])
	}
	if state["language"] != "go" || state["test_name"] != "TestCreateOrder" {
		t.Fatalf("state = %#v", state)
	}
	features, _ := state["features"].([]any)
	if len(features) != 1 || features[0] != evidence.FeatureGoTestFunction {
		t.Fatalf("features = %#v", state["features"])
	}
	if _, ok := state["annotations"].([]any); !ok {
		t.Fatalf("annotations should be a JSON array, got %#v", state["annotations"])
	}
	questions, ok := captured["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions = %#v", captured["questions"])
	}
	if _, ok := questions[PrimaryQuestionID]; !ok {
		t.Fatal("primary question missing")
	}
	for _, trait := range classification.NonDeterministicTraits() {
		if _, ok := questions[TraitQuestionID(trait)]; !ok {
			t.Fatalf("question for trait %q missing", trait)
		}
	}
	for _, trait := range classification.DeterministicTraits() {
		if _, ok := questions[TraitQuestionID(trait)]; ok {
			t.Fatalf("deterministic trait %q should not be asked", trait)
		}
	}
}

func TestClassifyTestsRejectsMalformedResponses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(decoded map[string]any)
		raw    string
	}{
		{name: "invalid JSON", raw: "not json"},
		{name: "missing model", mutate: func(d map[string]any) { delete(d, "model") }},
		{name: "missing answers", mutate: func(d map[string]any) { delete(d, "answers") }},
		{name: "missing trait answer", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			delete(answers, TraitQuestionID(classification.TraitRegression))
		}},
		{name: "unknown primary label", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			primary := answers[PrimaryQuestionID].(map[string]any)
			primary["choice"] = "functional"
		}},
		{name: "probabilities do not sum to one", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			primary := answers[PrimaryQuestionID].(map[string]any)
			probabilities := primary["probabilities"].(map[string]any)
			probabilities[string(classification.PrimaryUnit)] = 0.0
		}},
		{name: "missing confidence", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			primary := answers[PrimaryQuestionID].(map[string]any)
			delete(primary, "confidence")
		}},
		{name: "noul out of range", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			trait := answers[TraitQuestionID(classification.TraitRegression)].(map[string]any)
			trait["noul"] = 1.5
		}},
		{name: "wrong answer type", mutate: func(d map[string]any) {
			answers := d["answers"].(map[string]any)
			primary := answers[PrimaryQuestionID].(map[string]any)
			primary["type"] = "noul"
		}},
		{name: "trailing JSON", raw: `{"model":"jev-1.13.0","answers":{}} {"extra":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1)
			if tc.mutate != nil {
				decoded := decodeBody(t, body)
				tc.mutate(decoded)
				body = encodeBody(t, decoded)
			}
			if tc.raw != "" {
				body = []byte(tc.raw)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, body, http.StatusOK)
			}))
			defer server.Close()

			client := newTestClient(t, server)
			_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
			if err == nil {
				t.Fatal("expected malformed response error")
			}
			if !IsResponseError(err) {
				t.Fatalf("error = %v (%T), want ResponseError", err, err)
			}
		})
	}
}

func TestClassifyTestsAuthenticationFailure(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, []byte(`{"error":"bad key `+testAPIKey+`"}`), http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err == nil {
		t.Fatal("expected authentication error")
	}
	if !IsAuthenticationError(err) {
		t.Fatalf("error = %v, want authentication error", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("auth failure was retried: %d calls", calls)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaked the API key: %v", err)
	}
}

func TestClassifyTestsRedactsAPIKeyFromServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []byte(`{"error":"server rejected key `+testAPIKey+`"}`), http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err == nil {
		t.Fatal("expected server error")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaked the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error message was not redacted: %v", err)
	}
}

func TestClassifyTestsRetriesRateLimit(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			writeJSON(w, []byte(`{"error":"slow down"}`), http.StatusTooManyRequests)
			return
		}
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithMaxRetries(3))
	predictions, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if len(predictions) != 1 {
		t.Fatalf("got %d predictions", len(predictions))
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestClassifyTestsRetriesOverload(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			writeJSON(w, []byte(`{"error":"overloaded"}`), overloadedStatus)
			return
		}
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithMaxRetries(2))
	if _, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()}); err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestClassifyTestsStopsRetryingWhenExhausted(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, []byte(`{"error":"slow down"}`), http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithMaxRetries(2))
	_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %v (%T)", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (one attempt plus two retries)", got)
	}
}

func TestClassifyTestsTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithRequestTimeout(20*time.Millisecond), WithMaxRetries(0))
	_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !IsTimeoutError(err) {
		t.Fatalf("error = %v (%T), want timeout", err, err)
	}
}

func TestClassifyTestsRecordsReturnedModelForFloatingRequest(t *testing.T) {
	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeBody(t, body)
		requestedModel, _ = decoded["model"].(string)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithModel("jev-latest"), WithExperimentalModel(true))
	predictions, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{testEvidence()})
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if requestedModel != "jev-latest" {
		t.Fatalf("requested model = %q", requestedModel)
	}
	if predictions[0].Model != "jev-1.13.0" {
		t.Fatalf("recorded model = %q, want pinned response version", predictions[0].Model)
	}
}

func TestNewClientRejectsFloatingModel(t *testing.T) {
	if _, err := NewClient(testAPIKey, WithModel("jev-latest")); err == nil {
		t.Fatal("floating model was accepted without the experimental option")
	}
	if _, err := NewClient(testAPIKey, WithModel(DefaultModel)); err != nil {
		t.Fatalf("pinned model rejected: %v", err)
	}
	if _, err := NewClient(testAPIKey, WithModel("jev-latest"), WithExperimentalModel(true)); err != nil {
		t.Fatalf("experimental floating model rejected: %v", err)
	}
}

func TestClassifyTestsCombinesParserTraitsWithoutLosingProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.9), http.StatusOK)
	}))
	defer server.Close()

	ev := testEvidence()
	ev.Features = append(ev.Features, evidence.EvidenceFeature{
		Name:   evidence.FeatureGoParallelCall,
		Source: evidence.FeatureSourceParser,
	})
	client := newTestClient(t, server)
	predictions, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{ev})
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	prediction := predictions[0]
	concurrency := prediction.Traits[classification.TraitConcurrency]
	if !concurrency.Present || concurrency.Source != classification.TraitSourceParser {
		t.Fatalf("parser trait lost provenance: %+v", concurrency)
	}
	regression := prediction.Traits[classification.TraitRegression]
	if !regression.Present || regression.Source != classification.TraitSourceJev {
		t.Fatalf("jev trait provenance: %+v", regression)
	}
}

func TestClassifyTestsCombinesParserAndJevTraitDecisions(t *testing.T) {
	questions := DefaultQuestionSet()
	questions.Traits[classification.TraitConcurrency] = Question{
		Type:         "noul",
		Instructions: "Does this test exercise concurrency?",
	}
	decoded := decodeBody(t, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.2))
	answers := decoded["answers"].(map[string]any)
	answers[TraitQuestionID(classification.TraitConcurrency)] = map[string]any{"type": "noul", "noul": 0.9}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, encodeBody(t, decoded), http.StatusOK)
	}))
	defer server.Close()

	ev := testEvidence()
	ev.Features = append(ev.Features, evidence.EvidenceFeature{
		Name:   evidence.FeatureGoParallelCall,
		Source: evidence.FeatureSourceParser,
	})
	client := newTestClient(t, server, WithQuestionSet(questions))
	predictions, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{ev})
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	trait := predictions[0].Traits[classification.TraitConcurrency]
	if !trait.Present || trait.Source != classification.TraitSourceCombined {
		t.Fatalf("combined trait = %+v", trait)
	}
}

func TestClassifyTestsBoundsConcurrency(t *testing.T) {
	var inFlight, maxInFlight int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&inFlight, 1)
		for {
			observed := atomic.LoadInt32(&maxInFlight)
			if current <= observed || atomic.CompareAndSwapInt32(&maxInFlight, observed, current) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	tests := make([]evidence.TestEvidence, 5)
	for i := range tests {
		tests[i] = testEvidence()
		tests[i].ID.Qualified = tests[i].ID.Qualified + string(rune('A'+i))
	}
	client := newTestClient(t, server, WithConcurrency(2))
	if _, err := client.ClassifyTests(context.Background(), tests); err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if got := atomic.LoadInt32(&maxInFlight); got > 2 {
		t.Fatalf("max in flight = %d, want <= 2", got)
	}
}

func TestClassifyTestsRejectsOversizedBatchBeforeNetwork(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server, WithLimits(evidence.Limits{MaxTestsPerRun: 2}))
	tests := make([]evidence.TestEvidence, 3)
	_, err := client.ClassifyTests(context.Background(), tests)
	if err == nil {
		t.Fatal("expected limit error")
	}
	var limitErr *evidence.LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error = %v (%T), want LimitError", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("network was called %d times for an oversized batch", got)
	}
}

func TestClassifyTestsRejectsOversizedRequestBeforeNetwork(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	ev := testEvidence()
	ev.Source = strings.Repeat("x", 2048)
	client := newTestClient(t, server, withMaxRequestBytes(64), WithLimits(evidence.Limits{MaxSourceBytes: 1 << 20}))
	_, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{ev})
	if err == nil {
		t.Fatal("expected request-size limit error")
	}
	var limitErr *evidence.LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error = %v (%T), want LimitError", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("network was called %d times for an oversized request", got)
	}
}

func TestClassifyTestsTruncatesSourceBeforeNetwork(t *testing.T) {
	var capturedSource string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeBody(t, body)
		state, _ := decoded["state"].(map[string]any)
		capturedSource, _ = state["source"].(string)
		writeJSON(w, responseBody(t, "jev-1.13.0", string(classification.PrimaryUnit), 0.9, 0.1), http.StatusOK)
	}))
	defer server.Close()

	ev := testEvidence()
	ev.Source = strings.Repeat("x", 4096)
	client := newTestClient(t, server, WithLimits(evidence.Limits{MaxSourceBytes: 128}))
	if _, err := client.ClassifyTests(context.Background(), []evidence.TestEvidence{ev}); err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if len(capturedSource) > 128 {
		t.Fatalf("source length %d exceeds limit", len(capturedSource))
	}
}

func TestClassifyTestsEmptyInputMakesNoRequest(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	predictions, err := client.ClassifyTests(context.Background(), nil)
	if err != nil {
		t.Fatalf("ClassifyTests: %v", err)
	}
	if len(predictions) != 0 {
		t.Fatalf("predictions = %+v", predictions)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("empty input made a network call")
	}
}
