// Package jev implements the TypeSafe System One HTTP contract for the Jev
// classifier: a versioned question set, retry handling for 429 and 529, strict
// response validation, and pre-network byte and test-count limits.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// ClientVersion identifies the HTTP client for diagnostics.
const ClientVersion = "1.0.0"

// Default client settings.
const (
	DefaultBaseURL          = "https://api.typesafe.ai"
	DefaultConcurrency      = 4
	DefaultRequestTimeout   = 60 * time.Second
	DefaultMaxRetries       = 3
	DefaultBaseBackoff      = 500 * time.Millisecond
	DefaultMaxBackoff       = 8 * time.Second
	DefaultMaxRequestBytes  = 64 * 1024
	DefaultMaxResponseBytes = 1 << 20
	systemOnePath           = "/v1/systemone"
	overloadedStatus        = 529
)

// APIError is a non-retryable or exhausted HTTP error from the API. Messages are
// sanitized so they can never contain the API key.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("typesafe api error: status %d", e.StatusCode)
	}
	return fmt.Sprintf("typesafe api error: status %d: %s", e.StatusCode, e.Message)
}

// IsAuthenticationError reports whether err is an authentication failure.
func IsAuthenticationError(err error) bool {
	var target *APIError
	return errors.As(err, &target) && target.StatusCode == http.StatusUnauthorized
}

// ResponseError is a malformed or missing API response.
type ResponseError struct {
	Reason string
}

func (e *ResponseError) Error() string {
	return "malformed typesafe response: " + e.Reason
}

// TimeoutError reports that a request exceeded its per-request timeout.
type TimeoutError struct {
	Operation string
	Err       error
}

func (e *TimeoutError) Error() string {
	if e.Err == nil {
		return "typesafe request timed out: " + e.Operation
	}
	return "typesafe request timed out: " + e.Operation + ": " + e.Err.Error()
}

// Unwrap exposes the underlying error for errors.Is checks.
func (e *TimeoutError) Unwrap() error {
	return e.Err
}

// IsTimeoutError reports whether err is a request timeout.
func IsTimeoutError(err error) bool {
	var target *TimeoutError
	return errors.As(err, &target)
}

// Client is a TypeSafe System One client that implements TestClassifier.
type Client struct {
	httpClient       *http.Client
	baseURL          string
	apiKey           string
	model            string
	questions        QuestionSet
	experimental     bool
	concurrency      int
	requestTimeout   time.Duration
	maxRetries       int
	baseBackoff      time.Duration
	maxBackoff       time.Duration
	maxRequestBytes  int
	maxResponseBytes int64
	limits           evidence.Limits
	sleep            func(context.Context, time.Duration) error
	jitter           func(time.Duration) time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL, which supports testing and compatible
// proxies.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithModel sets the Jev model. Floating aliases require WithExperimentalModel.
func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

// WithExperimentalModel permits floating model aliases such as jev-latest.
func WithExperimentalModel(experimental bool) Option {
	return func(c *Client) { c.experimental = experimental }
}

// WithQuestionSet overrides the versioned question set.
func WithQuestionSet(questions QuestionSet) Option {
	return func(c *Client) { c.questions = questions }
}

// WithConcurrency bounds in-flight requests.
func WithConcurrency(concurrency int) Option {
	return func(c *Client) { c.concurrency = concurrency }
}

// WithRequestTimeout sets the per-request timeout.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.requestTimeout = timeout }
}

// WithMaxRetries sets the number of retries after the first attempt.
func WithMaxRetries(retries int) Option {
	return func(c *Client) { c.maxRetries = retries }
}

// WithHTTPClient overrides the underlying HTTP transport.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithLimits overrides the evidence byte and test-count limits.
func WithLimits(limits evidence.Limits) Option {
	return func(c *Client) { c.limits = limits }
}

// withMaxRequestBytes overrides the encoded-request byte limit. Tests use it to
// prove the limit is enforced before any network call.
func withMaxRequestBytes(maxBytes int) Option {
	return func(c *Client) { c.maxRequestBytes = maxBytes }
}

// withSleep overrides backoff sleeping. Tests use it to avoid real delays.
func withSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// withJitter overrides backoff jitter. Tests use it to make delays
// deterministic.
func withJitter(jitter func(time.Duration) time.Duration) Option {
	return func(c *Client) { c.jitter = jitter }
}

// NewClient builds a client. It rejects a floating model alias unless the
// experimental option is set.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	c := &Client{
		httpClient:       &http.Client{},
		baseURL:          DefaultBaseURL,
		apiKey:           apiKey,
		model:            DefaultModel,
		questions:        DefaultQuestionSet(),
		concurrency:      DefaultConcurrency,
		requestTimeout:   DefaultRequestTimeout,
		maxRetries:       DefaultMaxRetries,
		baseBackoff:      DefaultBaseBackoff,
		maxBackoff:       DefaultMaxBackoff,
		maxRequestBytes:  DefaultMaxRequestBytes,
		maxResponseBytes: DefaultMaxResponseBytes,
		limits:           evidence.DefaultLimits(),
		sleep:            sleepContext,
		jitter:           defaultJitter,
	}
	for _, opt := range opts {
		opt(c)
	}
	if err := ValidateModel(c.model, c.experimental); err != nil {
		return nil, err
	}
	if c.concurrency < 1 {
		c.concurrency = 1
	}
	return c, nil
}

// ValidateModel enforces a pinned model version unless the experimental option
// permits a floating alias.
func ValidateModel(model string, experimental bool) error {
	if strings.TrimSpace(model) == "" {
		return errors.New("jev model must not be empty")
	}
	if !experimental && isFloatingModel(model) {
		return fmt.Errorf("jev model %q is a floating alias; pin a versioned model or enable the experimental model option", model)
	}
	return nil
}

func isFloatingModel(model string) bool {
	return model == "latest" || strings.HasSuffix(model, "-latest")
}

// Model returns the configured model.
func (c *Client) Model() string {
	return c.model
}

// QuestionSetVersion returns the version of the configured question set.
func (c *Client) QuestionSetVersion() string {
	return c.questions.Version
}

// ClassifyTests classifies each test with one Jev request per test, bounded by
// the configured concurrency. Successful predictions are returned in input
// order; if any test fails, the successes are returned with a joined error.
func (c *Client) ClassifyTests(ctx context.Context, tests []evidence.TestEvidence) ([]classification.Prediction, error) {
	if err := c.limits.ValidateRun(tests); err != nil {
		return nil, err
	}
	if len(tests) == 0 {
		return nil, nil
	}
	semaphore := make(chan struct{}, c.concurrency)
	results := make([]classification.Prediction, len(tests))
	failures := make([]error, len(tests))

	var wg sync.WaitGroup
	for i := range tests {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				failures[index] = ctx.Err()
				return
			}
			defer func() { <-semaphore }()
			prediction, err := c.classifyOne(ctx, c.limits.Apply(tests[index]))
			results[index] = prediction
			failures[index] = err
		}(i)
	}
	wg.Wait()

	var (
		predictions []classification.Prediction
		errs        []error
	)
	for i := range tests {
		if failures[i] != nil {
			errs = append(errs, fmt.Errorf("%s: %w", tests[i].ID.String(), failures[i]))
			continue
		}
		predictions = append(predictions, results[i])
	}
	if len(errs) > 0 {
		return predictions, errors.Join(errs...)
	}
	return predictions, nil
}

func (c *Client) classifyOne(ctx context.Context, test evidence.TestEvidence) (classification.Prediction, error) {
	body, err := json.Marshal(c.buildRequest(test))
	if err != nil {
		return classification.Prediction{}, fmt.Errorf("encode jev request: %w", err)
	}
	if c.maxRequestBytes > 0 && len(body) > c.maxRequestBytes {
		return classification.Prediction{}, &evidence.LimitError{Subject: "request bytes", Limit: c.maxRequestBytes, Actual: len(body)}
	}
	response, err := c.postWithRetry(ctx, body)
	if err != nil {
		return classification.Prediction{}, err
	}
	return c.mapResponse(test, response)
}

type systemOneRequest struct {
	Model     string              `json:"model"`
	State     systemOneState      `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type systemOneState struct {
	Language    string   `json:"language"`
	TestName    string   `json:"test_name"`
	Annotations []string `json:"annotations"`
	Imports     []string `json:"imports"`
	Features    []string `json:"features"`
	Source      string   `json:"source"`
}

func (c *Client) buildRequest(test evidence.TestEvidence) systemOneRequest {
	name := test.Name
	if name == "" {
		name = test.ID.Qualified
	}
	features := make([]string, 0, len(test.Features))
	for _, feature := range test.Features {
		features = append(features, feature.Name)
	}
	return systemOneRequest{
		Model: c.model,
		State: systemOneState{
			Language:    string(test.Language),
			TestName:    name,
			Annotations: nonNilStrings(test.Annotations),
			Imports:     nonNilStrings(test.Imports),
			Features:    features,
			Source:      test.Source,
		},
		Questions: c.questions.Requests(),
	}
}

// postWithRetry retries only 429 and 529 responses, using exponential backoff
// with bounded jitter. Other errors are returned immediately.
func (c *Client) postWithRetry(ctx context.Context, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, c.backoff(attempt-1)); err != nil {
				return nil, c.contextError(ctx, err)
			}
		}
		response, retryable, err := c.postOnce(ctx, body)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, c.contextError(ctx, lastErr)
		}
	}
	return nil, lastErr
}

func (c *Client) postOnce(ctx context.Context, body []byte) ([]byte, bool, error) {
	requestCtx := ctx
	var cancel context.CancelFunc
	if c.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	url := strings.TrimRight(c.baseURL, "/") + systemOnePath
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build jev request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("User-Agent", "testclassify/"+ClientVersion)

	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, false, &TimeoutError{Operation: "systemone request", Err: err}
		}
		return nil, false, fmt.Errorf("systemone request: %w", err)
	}
	defer response.Body.Close()

	data, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if readErr != nil {
		return nil, false, fmt.Errorf("read systemone response: %w", readErr)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return nil, false, &ResponseError{Reason: "response exceeds size limit"}
	}

	switch {
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode == overloadedStatus:
		return nil, true, &APIError{StatusCode: response.StatusCode, Message: c.sanitize(errorMessage(data))}
	case response.StatusCode == http.StatusUnauthorized:
		return nil, false, &APIError{StatusCode: response.StatusCode, Message: "authentication failed"}
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, false, &APIError{StatusCode: response.StatusCode, Message: c.sanitize(errorMessage(data))}
	}
	return data, false, nil
}

func (c *Client) contextError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &TimeoutError{Operation: "systemone request", Err: ctx.Err()}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) backoff(attempt int) time.Duration {
	base := c.baseBackoff
	if base <= 0 {
		base = DefaultBaseBackoff
	}
	delay := base
	for i := 0; i < attempt; i++ {
		if delay > (1<<62)/2 {
			break
		}
		delay *= 2
	}
	if c.maxBackoff > 0 && delay > c.maxBackoff {
		delay = c.maxBackoff
	}
	if delay <= 0 {
		return 0
	}
	return c.jitter(delay)
}

func defaultJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sanitize removes the API key from any text that may be surfaced to a caller or
// a terminal.
func (c *Client) sanitize(text string) string {
	if c.apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, c.apiKey, "[redacted]")
}

// errorMessage extracts a short diagnostic from an error body without echoing
// unbounded data.
func errorMessage(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}
	var envelope struct {
		Error   any `json:"error"`
		Message any `json:"message"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil {
		if text := stringify(envelope.Error); text != "" {
			return text
		}
		if text := stringify(envelope.Message); text != "" {
			return text
		}
	}
	if len(trimmed) > 200 {
		return trimmed[:200]
	}
	return trimmed
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if text, ok := typed["message"].(string); ok {
			return text
		}
	}
	return ""
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
