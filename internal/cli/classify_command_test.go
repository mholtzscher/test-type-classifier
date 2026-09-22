package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
	"github.com/mholtzscher/test-type-classifier/internal/jev"
)

const testAPIKey = "test-key"

// validResponse builds a documented-shape System One response with every
// non-deterministic trait answered.
func validResponse(t *testing.T, primary string, confidence, noul float64) []byte {
	t.Helper()
	types := classification.PrimaryTestTypes()
	probabilities := make(map[string]float64, len(types))
	share := 0.3 / float64(len(types)-1)
	for _, candidate := range types {
		if string(candidate) == primary {
			probabilities[string(candidate)] = 0.7
			continue
		}
		probabilities[string(candidate)] = share
	}
	answers := map[string]any{
		jev.PrimaryQuestionID: map[string]any{
			"type":          "choice",
			"choice":        primary,
			"probabilities": probabilities,
			"confidence":    confidence,
		},
	}
	for _, trait := range classification.NonDeterministicTraits() {
		answers[jev.TraitQuestionID(trait)] = map[string]any{"type": "noul", "noul": noul}
	}
	body, err := json.Marshal(map[string]any{
		"model":   "jev-1.13.0",
		"answers": answers,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return body
}

// fakeAPI is a recording fake TypeSafe server.
type fakeAPI struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies []string
}

func newFakeAPI(t *testing.T, handler http.HandlerFunc) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		api.bodies = append(api.bodies, string(body))
		api.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeAPI) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.bodies)
}

func (a *fakeAPI) requestBodies() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.bodies...)
}

func classifyHandler(t *testing.T, primary string, confidence, noul float64) http.HandlerFunc {
	t.Helper()
	body := validResponse(t, primary, confidence, noul)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func envFunc(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// runCLI assembles a fake-server-backed environment and runs the command.
func runCLI(t *testing.T, api *fakeAPI, extraEnv map[string]string, args ...string) (int, string, string) {
	t.Helper()
	values := map[string]string{"TYPESAFE_API_KEY": testAPIKey}
	if api != nil {
		values["TYPESAFE_BASE_URL"] = api.server.URL
	}
	for key, value := range extraEnv {
		values[key] = value
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, envFunc(values), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeTempFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func tableLines(output string) []string {
	return strings.Split(strings.TrimRight(output, "\n"), "\n")
}

func TestRunClassifiesMixedLanguageFixture(t *testing.T) {
	api := newFakeAPI(t, classifyHandler(t, string(classification.PrimaryUnit), 0.9, 0.1))

	code, stdout, stderr := runCLI(t, api, nil, "--exclude", "*secret_test.go", "testdata/mixed")
	if code != ExitSuccess {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
	if got := strings.Count(stderr, SourceLeavesMachineNotice); got != 1 {
		t.Fatalf("notice count = %d, want 1\nstderr:\n%s", got, stderr)
	}

	lines := tableLines(stdout)
	if len(lines) != 4 {
		t.Fatalf("table lines = %d, want header plus three tests:\n%s", len(lines), stdout)
	}
	wantIDs := []string{
		"OrderTest.kt::OrderTest.creates order",
		"order_test.go::TestCreateOrder",
		"order_test.go::TestCreateOrder/database_failure",
	}
	for _, id := range wantIDs {
		if !strings.Contains(stdout, id) {
			t.Errorf("report is missing %q:\n%s", id, stdout)
		}
	}
	if !strings.Contains(stdout, "review") {
		t.Errorf("default policy should mark every row review:\n%s", stdout)
	}
	if got := api.calls(); got != 3 {
		t.Fatalf("server calls = %d, want 3", got)
	}
	for _, body := range api.requestBodies() {
		if strings.Contains(body, "EXCLUDED_TEST_SECRET_PAYLOAD") || strings.Contains(body, "SecretTest") {
			t.Fatalf("excluded file content reached the server:\n%s", body)
		}
	}
}

func TestRunAcceptsWhenThresholdsMet(t *testing.T) {
	api := newFakeAPI(t, classifyHandler(t, string(classification.PrimaryIntegration), 0.9, 0.1))

	code, stdout, stderr := runCLI(t, api, nil,
		"--exclude", "*secret_test.go",
		"--min-confidence", "0.5",
		"--min-margin", "0.1",
		"testdata/mixed")
	if code != ExitSuccess {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "accept") {
		t.Fatalf("thresholds should accept:\n%s", stdout)
	}
}

func TestRunContinuesPastParseFailureAndExitsNonzero(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "broken_test.go", "package broken\n\nfunc TestBroken(t *testing.T) {\n")
	writeTempFile(t, dir, "ok_test.go", "package broken\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")

	api := newFakeAPI(t, classifyHandler(t, string(classification.PrimaryUnit), 0.9, 0.1))
	code, stdout, stderr := runCLI(t, api, nil, dir)
	if code == ExitSuccess {
		t.Fatalf("parse failure should exit nonzero\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "ok_test.go::TestOK") {
		t.Fatalf("run did not continue past the parse failure:\n%s", stdout)
	}
	if !strings.Contains(stderr, "SKIPPED FILES") || !strings.Contains(stderr, "broken_test.go") {
		t.Fatalf("skipped files not reported:\n%s", stderr)
	}
	if got := api.calls(); got != 1 {
		t.Fatalf("server calls = %d, want 1", got)
	}
}

func TestRunAuthenticationFailureExitsNonzeroWithoutLeakingKey(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"rejected key ` + testAPIKey + `"}`))
	})
	code, _, stderr := runCLI(t, api, nil, "--exclude", "*secret_test.go", "testdata/mixed")
	if code == ExitSuccess {
		t.Fatal("authentication failure should exit nonzero")
	}
	if strings.Contains(stderr, testAPIKey) {
		t.Fatalf("stderr leaked the API key:\n%s", stderr)
	}
}

func TestRunMalformedResponseExitsNonzero(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0"}`))
	})
	code, _, stderr := runCLI(t, api, nil, "--exclude", "*secret_test.go", "testdata/mixed")
	if code == ExitSuccess {
		t.Fatalf("malformed response should exit nonzero\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "classification failed") {
		t.Fatalf("expected classification failure message:\n%s", stderr)
	}
}

func TestRunTimeoutExitsNonzero(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "ok_test.go", "package ok\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	response := validResponse(t, string(classification.PrimaryUnit), 0.9, 0.1)
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(response)
	})
	code, _, stderr := runCLI(t, api, nil, "--timeout", "20ms", dir)
	if code == ExitSuccess {
		t.Fatalf("timeout should exit nonzero\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "timed out") {
		t.Fatalf("expected a timeout message:\n%s", stderr)
	}
}

func TestRunMissingAPIKeyExitsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"testdata/mixed"}, envFunc(map[string]string{}), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "TYPESAFE_API_KEY") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsUnsupportedFormat(t *testing.T) {
	code, _, stderr := runCLI(t, nil, nil, "--format", "json", "testdata/mixed")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "unsupported format") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunRequiresBothThresholds(t *testing.T) {
	code, _, stderr := runCLI(t, nil, nil, "--min-confidence", "0.5", "testdata/mixed")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "both") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunFloatingModelRequiresExperimentalOption(t *testing.T) {
	api := newFakeAPI(t, classifyHandler(t, string(classification.PrimaryUnit), 0.9, 0.1))

	code, _, stderr := runCLI(t, api, nil, "--model", "jev-latest", "--exclude", "*secret_test.go", "testdata/mixed")
	if code != ExitUsage {
		t.Fatalf("floating model exit code = %d, want %d\nstderr:\n%s", code, ExitUsage, stderr)
	}
	if api.calls() != 0 {
		t.Fatalf("floating model made %d server calls before validation", api.calls())
	}

	code, _, stderr = runCLI(t, api, nil,
		"--model", "jev-latest",
		"--experimental-model",
		"--exclude", "*secret_test.go",
		"testdata/mixed")
	if code != ExitSuccess {
		t.Fatalf("experimental model exit code = %d\nstderr:\n%s", code, stderr)
	}
	bodies := api.requestBodies()
	if len(bodies) == 0 {
		t.Fatal("no request captured")
	}
	var request map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["model"] != "jev-latest" {
		t.Fatalf("request model = %v, want jev-latest", request["model"])
	}
}

func TestRunHelpExitsSuccess(t *testing.T) {
	code, _, _ := runCLI(t, nil, nil, "--help")
	if code != ExitSuccess {
		t.Fatalf("help exit code = %d, want %d", code, ExitSuccess)
	}
}

func TestRunNoTestsFound(t *testing.T) {
	api := newFakeAPI(t, classifyHandler(t, string(classification.PrimaryUnit), 0.9, 0.1))
	code, stdout, stderr := runCLI(t, api, nil, t.TempDir())
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr)
	}
	if !strings.HasPrefix(stdout, "TYPE") {
		t.Fatalf("expected header-only table:\n%s", stdout)
	}
	if !strings.Contains(stderr, "no tests found") {
		t.Fatalf("stderr = %q", stderr)
	}
	if api.calls() != 0 {
		t.Fatalf("no tests should make no server calls, got %d", api.calls())
	}
}
