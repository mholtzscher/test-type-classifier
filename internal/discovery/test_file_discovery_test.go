package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// newTree builds the shared discovery fixture and returns its root.
func newTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "pkg/order_test.go", "package pkg\n")
	writeFixture(t, root, "pkg/helper.go", "package pkg\n")
	writeFixture(t, root, "pkg/OrderTest.kt", "class OrderTest\n")
	writeFixture(t, root, "pkg/Helper.kt", "class Helper\n")
	writeFixture(t, root, "pkg/secret_test.go", "package pkg\n")
	writeFixture(t, root, "vendor/dep/dep_test.go", "package dep\n")
	writeFixture(t, root, "src/test/kotlin/Anything.kt", "class Anything\n")
	return root
}

func pathsOf(files []File) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDiscoverSelectsConventionalTestsDeterministically(t *testing.T) {
	root := newTree(t)
	files, err := New(nil, nil).Discover(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		"pkg/OrderTest.kt",
		"pkg/order_test.go",
		"pkg/secret_test.go",
		"src/test/kotlin/Anything.kt",
	}
	if got := pathsOf(files); !equalStrings(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for _, file := range files {
		if file.Language != evidence.LanguageGo && file.Language != evidence.LanguageKotlin {
			t.Fatalf("unexpected language for %s: %q", file.Path, file.Language)
		}
		if _, err := os.Stat(file.SourcePath); err != nil {
			t.Fatalf("source path %q is not readable: %v", file.SourcePath, err)
		}
	}
}

func TestDiscoverIsStableAcrossRootOrder(t *testing.T) {
	root := newTree(t)
	first, err := New(nil, nil).Discover(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Discover first: %v", err)
	}
	second, err := New(nil, nil).Discover(context.Background(), []string{root, filepath.Join(root, "pkg")})
	if err != nil {
		t.Fatalf("Discover second: %v", err)
	}
	if !equalStrings(pathsOf(first), pathsOf(second)) {
		t.Fatalf("root order changed output: %v vs %v", pathsOf(first), pathsOf(second))
	}
}

func TestDiscoverExcludesBeforeReturns(t *testing.T) {
	root := newTree(t)
	files, err := New(nil, []string{"*secret_test.go"}).Discover(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, file := range files {
		if file.Path == "pkg/secret_test.go" {
			t.Fatalf("excluded file was discovered: %v", pathsOf(files))
		}
	}
}

func TestDiscoverExcludesDirectorySubtree(t *testing.T) {
	root := newTree(t)
	files, err := New(nil, []string{"src/**"}).Discover(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, file := range files {
		if file.Path == "src/test/kotlin/Anything.kt" {
			t.Fatalf("excluded subtree was discovered: %v", pathsOf(files))
		}
	}
}

func TestDiscoverIncludeAddsNonConventionalFile(t *testing.T) {
	root := newTree(t)
	files, err := New([]string{"helper.go"}, nil).Discover(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	found := false
	for _, file := range files {
		if file.Path == "pkg/helper.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("included file missing: %v", pathsOf(files))
	}
}

func TestDiscoverDeduplicatesOverlappingRoots(t *testing.T) {
	root := newTree(t)
	files, err := New(nil, nil).Discover(context.Background(), []string{root, filepath.Join(root, "pkg")})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	count := 0
	for _, file := range files {
		if file.Path == "pkg/order_test.go" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("order_test.go discovered %d times: %v", count, pathsOf(files))
	}
}

func TestDiscoverSingleFileRoot(t *testing.T) {
	root := newTree(t)
	single := filepath.Join(root, "pkg", "order_test.go")
	files, err := New(nil, nil).Discover(context.Background(), []string{single})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 1 || files[0].Path != filepath.ToSlash(single) || files[0].Language != evidence.LanguageGo {
		t.Fatalf("single file discovery = %+v", files)
	}
}

func TestDiscoverMissingRootIsAnError(t *testing.T) {
	if _, err := New(nil, nil).Discover(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestDiscoverDefaultsToCurrentDirectory(t *testing.T) {
	root := newTree(t)
	t.Chdir(root)
	files, err := New(nil, nil).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !equalStrings(pathsOf(files), []string{
		"pkg/OrderTest.kt",
		"pkg/order_test.go",
		"pkg/secret_test.go",
		"src/test/kotlin/Anything.kt",
	}) {
		t.Fatalf("default root paths = %v", pathsOf(files))
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "*_test.go", name: "pkg/order_test.go", want: true},
		{pattern: "order_test.go", name: "pkg/order_test.go", want: true},
		{pattern: "pkg/*_test.go", name: "pkg/order_test.go", want: true},
		{pattern: "pkg/**", name: "pkg/deep/order_test.go", want: true},
		{pattern: "**/*_test.go", name: "a/b/order_test.go", want: true},
		{pattern: "pkg/*.go", name: "other/order_test.go", want: false},
		{pattern: "pkg/order.go", name: "pkg/order_test.go", want: false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
