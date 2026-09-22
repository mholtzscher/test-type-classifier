// Package discovery finds Go and Kotlin test files under one or more roots. It
// performs only path walking and language detection: it never reads file
// contents, so an excluded path can never reach a parser or leave the machine.
package discovery

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// DefaultIgnoredDirs are directory names skipped while walking. They hold
// dependency caches, build output, or VCS metadata rather than first-party test
// source. A root that names one of these directories directly is still walked,
// so an explicit path always wins over the default.
var DefaultIgnoredDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	".gradle":      true,
	".idea":        true,
	"node_modules": true,
	"vendor":       true,
	"build":        true,
	"target":       true,
	"out":          true,
}

// kotlinTestSuffixes are the conventional Kotlin test class name endings. The
// comparison is case-sensitive so a class such as "Latest" is not mistaken for
// a test named "test".
var kotlinTestSuffixes = []string{"Test", "Tests", "TestSuite", "TestCase", "Spec"}

// kotlinTestDirs are lowercased, slash-delimited path fragments that mark a
// conventional Kotlin test source root regardless of file name.
var kotlinTestDirs = []string{
	"/src/test/",
	"/src/androidtest/",
	"/src/integrationtest/",
	"/test/kotlin/",
}

// File is one discovered source file that may contain tests.
type File struct {
	// Path is the cleaned, slash-separated path reported to parsers and in the
	// final report. It is relative to the discovery root that first found it.
	Path string
	// SourcePath is the filesystem path used to open the file. It keeps the
	// walked form, so it may be absolute when the root is absolute.
	SourcePath string
	// Language is the parser family selected from the file extension.
	Language evidence.Language
}

// Discoverer walks roots and selects candidate test files. Include patterns add
// candidates beyond the language conventions; exclude patterns remove paths
// before any file is opened. The zero value uses no extra patterns.
type Discoverer struct {
	include []string
	exclude []string
}

// New returns a Discoverer for the given repeatable include and exclude
// patterns. Patterns may be globs; a pattern without a slash matches a base
// name, and "**" matches any number of path segments.
func New(include, exclude []string) *Discoverer {
	return &Discoverer{
		include: normalizePatterns(include),
		exclude: normalizePatterns(exclude),
	}
}

// Discover returns every candidate file under roots, sorted by path and with
// duplicate files removed. With no roots it searches the current directory.
func (d *Discoverer) Discover(ctx context.Context, roots []string) ([]File, error) {
	if len(roots) == 0 {
		roots = []string{"."}
	}
	seen := make(map[string]bool)
	var files []File
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := d.walk(ctx, root, seen, &files); err != nil {
			return nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (d *Discoverer) walk(ctx context.Context, root string, seen map[string]bool, files *[]File) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("discovery root %s: %w", root, err)
	}
	if !info.IsDir() {
		return d.consider(root, displayPath(root, root), seen, files)
	}
	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := displayPath(root, p)
		if entry.IsDir() {
			if p != root && DefaultIgnoredDirs[entry.Name()] {
				return filepath.SkipDir
			}
			if d.excluded(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		return d.consider(p, rel, seen, files)
	})
	if err != nil {
		return fmt.Errorf("discovery walk %s: %w", root, err)
	}
	return nil
}

func (d *Discoverer) consider(p, rel string, seen map[string]bool, files *[]File) error {
	language, ok := detectLanguage(p)
	if !ok {
		return nil
	}
	if d.excluded(rel) {
		return nil
	}
	if !d.included(rel) && !isConventionalTest(rel, language) {
		return nil
	}
	key, err := filepath.Abs(p)
	if err != nil {
		key = filepath.Clean(p)
	}
	if seen[key] {
		return nil
	}
	seen[key] = true
	*files = append(*files, File{Path: filepath.ToSlash(rel), SourcePath: p, Language: language})
	return nil
}

// detectLanguage selects a parser family from the file extension.
func detectLanguage(p string) (evidence.Language, bool) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go":
		return evidence.LanguageGo, true
	case ".kt":
		return evidence.LanguageKotlin, true
	default:
		return "", false
	}
}

// isConventionalTest reports whether a path matches a language test convention.
// Include patterns are checked separately and can add anything else.
func isConventionalTest(rel string, language evidence.Language) bool {
	slashed := filepath.ToSlash(rel)
	base := path.Base(slashed)
	switch language {
	case evidence.LanguageGo:
		return strings.HasSuffix(base, "_test.go")
	case evidence.LanguageKotlin:
		if !strings.HasSuffix(base, ".kt") {
			return false
		}
		stem := strings.TrimSuffix(base, ".kt")
		for _, suffix := range kotlinTestSuffixes {
			if strings.HasSuffix(stem, suffix) {
				return true
			}
		}
		lower := "/" + strings.ToLower(slashed)
		for _, marker := range kotlinTestDirs {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// displayPath renders a walked path relative to its discovery root. The current
// directory root keeps the walked form so "./x" and "x" are reported as "x".
func displayPath(root, p string) string {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(p)
	if cleanRoot == "." || cleanRoot == "" {
		return filepath.ToSlash(cleanPath)
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(cleanPath)
	}
	return filepath.ToSlash(rel)
}

func (d *Discoverer) included(rel string) bool {
	for _, pattern := range d.include {
		if globMatch(pattern, rel) {
			return true
		}
	}
	return false
}

func (d *Discoverer) excluded(rel string) bool {
	for _, pattern := range d.exclude {
		if globMatch(pattern, rel) {
			return true
		}
	}
	return false
}

func normalizePatterns(patterns []string) []string {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
		normalized = append(normalized, pattern)
	}
	return normalized
}

// globMatch matches a slash path against a pattern. A pattern without a slash
// matches only the base name. A pattern with a slash matches the full path or
// any trailing subpath so "pkg/foo" also matches "a/b/pkg/foo". The "**"
// segment matches zero or more path segments.
func globMatch(pattern, name string) bool {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	if !strings.Contains(pattern, "/") {
		matched, err := path.Match(pattern, path.Base(name))
		return err == nil && matched
	}
	patternSegments := strings.Split(pattern, "/")
	if matchSegments(patternSegments, strings.Split(name, "/")) {
		return true
	}
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		if matchSegments(patternSegments, parts[i:]) {
			return true
		}
	}
	return false
}

func matchSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegments(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], name[0])
	if err != nil || !matched {
		return false
	}
	return matchSegments(pattern[1:], name[1:])
}
