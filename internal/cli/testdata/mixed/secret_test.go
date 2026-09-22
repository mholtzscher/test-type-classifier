package mixed

import "testing"

// SecretTest is excluded from discovery by the CLI fixture; its body must never
// reach the classification server.
func SecretTest(t *testing.T) {
	const marker = "EXCLUDED_TEST_SECRET_PAYLOAD"
	if marker == "" {
		t.Fatal("unreachable")
	}
}
