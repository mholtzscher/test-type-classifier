// Package goast extracts Go tests, fuzz targets, benchmarks, and nested subtests
// from source using go/parser. It resolves no packages or symbols: evidence is
// limited to syntax, imports, and deterministic call patterns.
package goast

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// ParserVersion identifies the extraction rules so evaluation records can be
// traced back to a parser revision.
const ParserVersion = "goast-1.0.0"

// Parser extracts Go test evidence.
type Parser struct {
	// Limits bounds source bytes and list lengths. The zero value uses
	// evidence.DefaultLimits.
	Limits evidence.Limits
}

// NewParser returns a Parser with the default evidence limits.
func NewParser() *Parser {
	return &Parser{Limits: evidence.DefaultLimits()}
}

// ParseTests parses one Go source file and returns one TestEvidence per test,
// fuzz target, benchmark, and nested subtest.
func (p *Parser) ParseTests(ctx context.Context, path string, source []byte) ([]evidence.TestEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits := p.limits()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	imports := importPaths(file)
	importFeatures := evidence.ImportFeatures(imports)

	var tests []evidence.TestEvidence
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		kind, param := classifyFunc(fd)
		if kind == kindNone {
			continue
		}
		tests = append(tests, p.extractFunction(fset, path, source, fd, kind, param, imports, importFeatures, limits)...)
	}
	return tests, nil
}

func (p *Parser) limits() evidence.Limits {
	if p == nil || p.Limits.MaxSourceBytes <= 0 {
		return evidence.DefaultLimits()
	}
	return p.Limits
}

type testKind int

const (
	kindNone testKind = iota
	kindTest
	kindFuzz
	kindBenchmark
)

// classifyFunc recognizes the go test entry points accepted by the testing
// package. TestMain is excluded because its parameter is *testing.M.
func classifyFunc(fd *ast.FuncDecl) (testKind, string) {
	if fd.Body == nil {
		return kindNone, ""
	}
	name := fd.Name.Name
	if strings.HasPrefix(name, "Test") {
		if param := testParamName(fd, "T"); param != "" {
			return kindTest, param
		}
	}
	if strings.HasPrefix(name, "Fuzz") {
		if param := testParamName(fd, "F"); param != "" {
			return kindFuzz, param
		}
	}
	if strings.HasPrefix(name, "Benchmark") {
		if param := testParamName(fd, "B"); param != "" {
			return kindBenchmark, param
		}
	}
	return kindNone, ""
}

func testParamName(fd *ast.FuncDecl, typeName string) string {
	if fd.Type == nil || fd.Type.Params == nil {
		return ""
	}
	for _, field := range fd.Type.Params.List {
		if len(field.Names) != 1 {
			continue
		}
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "testing" || sel.Sel.Name != typeName {
			continue
		}
		return field.Names[0].Name
	}
	return ""
}

func (p *Parser) extractFunction(
	fset *token.FileSet,
	path string,
	source []byte,
	fd *ast.FuncDecl,
	kind testKind,
	param string,
	imports []string,
	importFeatures []evidence.EvidenceFeature,
	limits evidence.Limits,
) []evidence.TestEvidence {
	name := fd.Name.Name
	scan := scanBody(fd.Body, param, kind)
	top := evidence.TestEvidence{
		ID:       evidence.TestID{Path: path, Qualified: name},
		Language: evidence.LanguageGo,
		Name:     name,
		Imports:  imports,
		Source:   nodeSource(fset, source, fd),
		Features: mergeFeatures(scan.features, importFeatures),
	}
	tests := []evidence.TestEvidence{limits.Apply(top)}
	tests = append(tests, p.extractSubtests(fset, path, source, fd.Body, param, name, imports, importFeatures, limits)...)
	return tests
}

// extractSubtests walks t.Run calls directly in body (not inside nested
// closures) and recurses into each subtest closure so nested subtests keep their
// parent-qualified name.
func (p *Parser) extractSubtests(
	fset *token.FileSet,
	path string,
	source []byte,
	body *ast.BlockStmt,
	param string,
	prefix string,
	imports []string,
	importFeatures []evidence.EvidenceFeature,
	limits evidence.Limits,
) []evidence.TestEvidence {
	var tests []evidence.TestEvidence
	for _, run := range directRunCalls(body, param) {
		if len(run.call.Args) < 2 {
			continue
		}
		lit, ok := run.call.Args[1].(*ast.FuncLit)
		if !ok || lit.Body == nil {
			continue
		}
		subParam := firstParamName(lit)
		subName := subtestName(fset, source, run.call.Args[0])
		qualified := subName
		if prefix != "" {
			qualified = prefix + "/" + subName
		}
		scan := scanBody(lit.Body, subParam, kindNone)
		features := scan.features
		if run.inRange {
			features = addFeature(features, evidence.FeatureGoTableDrivenSubtest)
		}
		child := evidence.TestEvidence{
			ID:       evidence.TestID{Path: path, Qualified: qualified},
			Language: evidence.LanguageGo,
			Name:     qualified,
			Imports:  imports,
			Source:   nodeSource(fset, source, lit),
			Features: mergeFeatures(features, importFeatures),
		}
		tests = append(tests, limits.Apply(child))
		tests = append(tests, p.extractSubtests(fset, path, source, lit.Body, subParam, qualified, imports, importFeatures, limits)...)
	}
	return tests
}

type runCall struct {
	call    *ast.CallExpr
	inRange bool
}

// directRunCalls returns t.Run calls lexically inside body but stops at any
// function literal, so each level of subtest nesting is handled once.
func directRunCalls(body *ast.BlockStmt, param string) []runCall {
	if body == nil || param == "" {
		return nil
	}
	var ranges []nodeSpan
	ast.Inspect(body, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok {
			ranges = append(ranges, nodeSpan{start: r.Pos(), end: r.End()})
		}
		return true
	})
	var calls []runCall
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isRunCall(call, param) {
			return true
		}
		calls = append(calls, runCall{call: call, inRange: pointInAny(call.Pos(), ranges)})
		return false
	})
	return calls
}

func isRunCall(call *ast.CallExpr, param string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Run" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == param
}

func firstParamName(lit *ast.FuncLit) string {
	if lit.Type == nil || lit.Type.Params == nil {
		return ""
	}
	for _, field := range lit.Type.Params.List {
		if len(field.Names) > 0 {
			return field.Names[0].Name
		}
	}
	return ""
}

func subtestName(fset *token.FileSet, source []byte, expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if value, err := strconv.Unquote(lit.Value); err == nil {
			return value
		}
		return strings.Trim(lit.Value, "`\"")
	}
	return nodeSource(fset, source, expr)
}

var (
	goldenCallPattern   = regexp.MustCompile(`(?i)golden`)
	snapshotCallPattern = regexp.MustCompile(`(?i)snapshot`)
)

type bodyScan struct {
	features []evidence.EvidenceFeature
}

// scanBody collects deterministic call and definition features from one block.
func scanBody(body *ast.BlockStmt, param string, kind testKind) bodyScan {
	var scan bodyScan
	if body == nil {
		return scan
	}
	seen := make(map[string]bool)
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		scan.features = append(scan.features, evidence.EvidenceFeature{Name: name, Source: evidence.FeatureSourceParser})
	}
	switch kind {
	case kindTest:
		add(evidence.FeatureGoTestFunction)
	case kindFuzz:
		add(evidence.FeatureGoFuzzFunction)
	case kindBenchmark:
		add(evidence.FeatureGoBenchmarkFunction)
	}
	if param == "" {
		return scan
	}
	var ranges []nodeSpan
	ast.Inspect(body, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok {
			ranges = append(ranges, nodeSpan{start: r.Pos(), end: r.End()})
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == param {
				switch sel.Sel.Name {
				case "Run":
					add(evidence.FeatureGoSubtestCall)
					if pointInAny(call.Pos(), ranges) {
						add(evidence.FeatureGoTableDrivenSubtest)
					}
				case "Parallel":
					add(evidence.FeatureGoParallelCall)
				}
			}
		}
		name := calleeName(call)
		if goldenCallPattern.MatchString(name) {
			add(evidence.FeatureGoldenComparison)
		}
		if snapshotCallPattern.MatchString(name) {
			add(evidence.FeatureSnapshotAssertion)
		}
		return true
	})
	return scan
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func importPaths(file *ast.File) []string {
	var paths []string
	for _, spec := range file.Imports {
		if spec.Path == nil {
			continue
		}
		if path, err := strconv.Unquote(spec.Path.Value); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

func nodeSource(fset *token.FileSet, source []byte, node ast.Node) string {
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset
	if start < 0 || end > len(source) || start >= end {
		return ""
	}
	return string(source[start:end])
}

type nodeSpan struct {
	start token.Pos
	end   token.Pos
}

func pointInAny(pos token.Pos, spans []nodeSpan) bool {
	for _, s := range spans {
		if pos >= s.start && pos <= s.end {
			return true
		}
	}
	return false
}

func addFeature(features []evidence.EvidenceFeature, name string) []evidence.EvidenceFeature {
	for _, feature := range features {
		if feature.Name == name {
			return features
		}
	}
	return append(features, evidence.EvidenceFeature{Name: name, Source: evidence.FeatureSourceParser})
}

// mergeFeatures appends extras without duplicating names, keeping the
// structural features first.
func mergeFeatures(base, extras []evidence.EvidenceFeature) []evidence.EvidenceFeature {
	merged := append([]evidence.EvidenceFeature(nil), base...)
	for _, feature := range extras {
		merged = addFeature(merged, feature.Name)
	}
	return merged
}
