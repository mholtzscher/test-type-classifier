// Package kotlinast extracts Kotlin JUnit and parameterized tests with a
// pure-Go, syntax-focused scanner. It deliberately avoids Tree-sitter so the CLI
// stays a static binary with no CGO, at the cost of not resolving types or
// symbols.
package kotlinast

import (
	"context"
	"regexp"
	"strings"

	"github.com/mholtzscher/test-type-classifier/internal/evidence"
)

// ParserVersion identifies the extraction rules so evaluation records can be
// traced back to a parser revision.
const ParserVersion = "kotlinast-1.0.0"

// Parser extracts Kotlin test evidence.
type Parser struct {
	// Limits bounds source bytes and list lengths. The zero value uses
	// evidence.DefaultLimits.
	Limits evidence.Limits
}

// NewParser returns a Parser with the default evidence limits.
func NewParser() *Parser {
	return &Parser{Limits: evidence.DefaultLimits()}
}

// testAnnotations are the JUnit annotations that mark a function as a test.
var testAnnotations = map[string]bool{
	"Test":              true,
	"ParameterizedTest": true,
	"RepeatedTest":      true,
	"TestFactory":       true,
	"TestTemplate":      true,
}

// parameterizedSourceAnnotations provide the arguments for a parameterized test.
var parameterizedSourceAnnotations = map[string]bool{
	"ValueSource":        true,
	"MethodSource":       true,
	"CsvSource":          true,
	"CsvFileSource":      true,
	"EnumSource":         true,
	"NullSource":         true,
	"EmptySource":        true,
	"NullAndEmptySource": true,
	"ArgumentsSource":    true,
}

// ParseTests returns one TestEvidence per annotated test function. Tests without
// a recognized JUnit annotation are skipped because Kotlin has no naming
// convention to fall back on.
func (p *Parser) ParseTests(ctx context.Context, path string, source []byte) ([]evidence.TestEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits := p.limits()
	parser := &kotlinParser{
		src:       string(source),
		tokens:    lexKotlin(string(source)),
		classOpen: make(map[int]string),
		limits:    limits,
	}
	imports := parser.collectImports()
	importFeatures := evidence.ImportFeatures(imports)
	tests := parser.parse(path, imports, importFeatures)
	return tests, nil
}

func (p *Parser) limits() evidence.Limits {
	if p == nil || p.Limits.MaxSourceBytes <= 0 {
		return evidence.DefaultLimits()
	}
	return p.Limits
}

type annotation struct {
	simple   string
	raw      string
	startOff int
}

type classFrame struct {
	name  string
	depth int
}

type kotlinParser struct {
	src       string
	tokens    []token
	pos       int
	classes   []classFrame
	depth     int
	pending   []annotation
	classOpen map[int]string
	limits    evidence.Limits
}

// collectImports walks the token stream once to gather imports. It runs before
// the main parse so import features are available to every test.
func (p *kotlinParser) collectImports() []string {
	var imports []string
	for i := 0; i < len(p.tokens); i++ {
		if p.tokens[i].kind == tokIdent && p.tokens[i].text == "import" {
			var sb strings.Builder
			j := i + 1
			for j < len(p.tokens) && p.tokens[j].kind != tokNewline && p.tokens[j].kind != tokEOF {
				if p.tokens[j].kind == tokIdent && p.tokens[j].text == "as" {
					break
				}
				sb.WriteString(p.tokens[j].text)
				j++
			}
			spec := strings.TrimSuffix(strings.TrimSpace(sb.String()), ";")
			if spec != "" {
				imports = append(imports, spec)
			}
			i = j
		}
	}
	return imports
}

func (p *kotlinParser) parse(path string, imports []string, importFeatures []evidence.EvidenceFeature) []evidence.TestEvidence {
	var tests []evidence.TestEvidence
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		switch {
		case tok.kind == tokEOF:
			return tests
		case tok.kind == tokNewline:
			count := 0
			for p.pos < len(p.tokens) && p.tokens[p.pos].kind == tokNewline {
				count++
				p.pos++
			}
			if count >= 2 {
				p.pending = nil
			}
		case tok.kind == tokIdent && tok.text == "import":
			for p.pos < len(p.tokens) && p.tokens[p.pos].kind != tokNewline {
				p.pos++
			}
		case tok.text == "@":
			p.parseAnnotation()
		case tok.kind == tokIdent && (tok.text == "class" || tok.text == "object"):
			p.parseClass()
			p.pending = nil
		case tok.kind == tokIdent && tok.text == "fun":
			if test, ok := p.parseFunction(path, imports, importFeatures); ok {
				tests = append(tests, test)
			}
			p.pending = nil
		case tok.text == "{":
			if name, ok := p.classOpen[p.pos]; ok {
				p.classes = append(p.classes, classFrame{name: name, depth: p.depth + 1})
			}
			p.depth++
			p.pending = nil
			p.pos++
		case tok.text == "}":
			p.depth--
			for len(p.classes) > 0 && p.classes[len(p.classes)-1].depth > p.depth {
				p.classes = p.classes[:len(p.classes)-1]
			}
			p.pending = nil
			p.pos++
		case tok.kind == tokIdent && (tok.text == "val" || tok.text == "var" || tok.text == "typealias"):
			p.pending = nil
			p.pos++
		default:
			p.pos++
		}
	}
	return tests
}

// parseAnnotation captures one annotation, including a use-site target such as
// @file:JvmName, and any balanced argument list.
func (p *kotlinParser) parseAnnotation() {
	start := p.tokens[p.pos].off
	p.pos++
	simple := p.readQualifiedName()
	if p.pos < len(p.tokens) && p.tokens[p.pos].text == ":" {
		p.pos++
		if target := p.readQualifiedName(); target != "" {
			simple = target
		}
	}
	if p.pos < len(p.tokens) && p.tokens[p.pos].text == "(" {
		p.skipBalanced("(", ")")
	}
	end := start
	if p.pos > 0 && p.tokens[p.pos-1].end > start {
		end = p.tokens[p.pos-1].end
	}
	p.pending = append(p.pending, annotation{
		simple:   simple,
		raw:      strings.TrimSpace(p.src[start:end]),
		startOff: start,
	})
}

func (p *kotlinParser) readQualifiedName() string {
	simple := ""
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		if tok.kind != tokIdent {
			break
		}
		simple = tok.value
		p.pos++
		if p.pos < len(p.tokens) && p.tokens[p.pos].text == "." {
			p.pos++
			continue
		}
		break
	}
	return simple
}

func (p *kotlinParser) parseClass() {
	p.pos++ // class / object
	for p.pos < len(p.tokens) && p.tokens[p.pos].kind == tokNewline {
		p.pos++
	}
	if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != tokIdent {
		return
	}
	name := p.tokens[p.pos].value
	p.pos++
	if body, ok := findClassBody(p.tokens, p.pos); ok {
		p.classOpen[body] = name
	}
}

// findClassBody looks ahead for the class body brace, skipping type parameters,
// a primary constructor, and a supertype list. It stops when a new declaration
// begins so a bodyless class cannot borrow the next declaration's brace.
func findClassBody(tokens []token, i int) (int, bool) {
	paren, angle := 0, 0
	for i < len(tokens) {
		tok := tokens[i]
		if tok.kind == tokNewline {
			i++
			continue
		}
		switch tok.text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		case "{":
			if paren == 0 && angle == 0 {
				return i, true
			}
		case "}":
			return 0, false
		}
		if paren == 0 && angle == 0 {
			if tok.kind == tokIdent {
				switch tok.text {
				case "fun", "val", "var", "class", "object", "typealias", "interface":
					return 0, false
				}
			}
			if tok.text == "@" || tok.text == ";" || tok.text == "=" {
				return 0, false
			}
		}
		i++
	}
	return 0, false
}

// parseFunction parses a function signature and body, then emits evidence when
// the function carries a JUnit test annotation. It advances past the body so the
// outer loop never descends into function-local braces.
func (p *kotlinParser) parseFunction(path string, imports []string, importFeatures []evidence.EvidenceFeature) (evidence.TestEvidence, bool) {
	startOff := p.tokens[p.pos].off
	if len(p.pending) > 0 {
		startOff = p.pending[0].startOff
	}
	p.pos++ // fun

	if p.pos < len(p.tokens) && p.tokens[p.pos].text == "<" {
		p.skipAngle()
	}

	nameIdx := findFunctionName(p.tokens, p.pos)
	if nameIdx < 0 {
		return evidence.TestEvidence{}, false
	}
	name := p.tokens[nameIdx].value
	p.pos = nameIdx + 1

	if p.pos < len(p.tokens) && p.tokens[p.pos].text == "(" {
		p.skipBalanced("(", ")")
	}

	bodyStart := -1
	angle, paren, brack := 0, 0, 0
	j := p.pos
scan:
	for j < len(p.tokens) {
		tok := p.tokens[j]
		switch tok.text {
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			brack++
		case "]":
			if brack > 0 {
				brack--
			}
		case "{":
			if angle == 0 && paren == 0 && brack == 0 {
				bodyStart = j
				break scan
			}
		case "=":
			if angle == 0 && paren == 0 && brack == 0 {
				break scan
			}
		}
		if tok.kind == tokNewline && angle == 0 && paren == 0 && brack == 0 {
			break
		}
		j++
	}

	var endOff int
	var bodyStartIdx, bodyEndIdx = -1, -1
	switch {
	case bodyStart >= 0:
		bodyStartIdx = bodyStart
		bodyEndIdx = matchingBrace(p.tokens, bodyStart)
		if bodyEndIdx < 0 {
			bodyEndIdx = bodyStart
		}
		endOff = p.tokens[bodyEndIdx].end
		p.pos = bodyEndIdx + 1
	default:
		last := p.pos
		for last < len(p.tokens) && p.tokens[last].kind != tokNewline {
			last++
		}
		if last > p.pos {
			endOff = p.tokens[last-1].end
		} else {
			endOff = p.tokens[p.pos-1].end
		}
		p.pos = last
	}

	annotationNames := make([]string, 0, len(p.pending))
	annotationTexts := make([]string, 0, len(p.pending))
	for _, a := range p.pending {
		annotationNames = append(annotationNames, a.simple)
		annotationTexts = append(annotationTexts, a.raw)
	}
	if !isTestFunction(annotationNames) {
		return evidence.TestEvidence{}, false
	}

	qualified := name
	if len(p.classes) > 0 {
		names := make([]string, 0, len(p.classes))
		for _, frame := range p.classes {
			names = append(names, frame.name)
		}
		qualified = strings.Join(names, ".") + "." + name
	}

	features := kotlinFeatures(annotationNames, p.tokens, bodyStartIdx, bodyEndIdx)
	features = mergeFeatures(features, importFeatures)

	source := strings.TrimSpace(p.src[startOff:endOff])
	return p.limits.Apply(evidence.TestEvidence{
		ID:          evidence.TestID{Path: path, Qualified: qualified},
		Language:    evidence.LanguageKotlin,
		Name:        qualified,
		Annotations: annotationTexts,
		Imports:     imports,
		Source:      source,
		Features:    features,
	}), true
}

// findFunctionName returns the token index of the function name: the first
// identifier immediately followed by an opening parenthesis. This skips type
// parameters and a receiver type such as Foo.bar.
func findFunctionName(tokens []token, i int) int {
	for i < len(tokens) {
		tok := tokens[i]
		if tok.kind == tokEOF {
			return -1
		}
		if tok.kind == tokNewline {
			i++
			continue
		}
		if tok.text == "{" || tok.text == "=" || tok.text == ";" {
			return -1
		}
		if tok.kind == tokIdent {
			next := nextSignificant(tokens, i+1)
			if next >= 0 && tokens[next].text == "(" {
				return i
			}
		}
		i++
	}
	return -1
}

func isTestFunction(names []string) bool {
	for _, name := range names {
		if testAnnotations[name] {
			return true
		}
	}
	return false
}

var (
	goldenCallPattern   = regexp.MustCompile(`(?i)golden`)
	snapshotCallPattern = regexp.MustCompile(`(?i)snapshot`)
)

// kotlinFeatures derives deterministic features from annotations and body call
// names. bodyStart and bodyEnd are token indices, or -1 when the function has an
// expression body or no body.
func kotlinFeatures(names []string, tokens []token, bodyStart, bodyEnd int) []evidence.EvidenceFeature {
	var features []evidence.EvidenceFeature
	seen := make(map[string]bool)
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		features = append(features, evidence.EvidenceFeature{Name: name, Source: evidence.FeatureSourceParser})
	}
	for _, name := range names {
		switch name {
		case "Test":
			add(evidence.FeatureKotlinTestAnnotation)
		case "ParameterizedTest":
			add(evidence.FeatureKotlinParameterizedAnnotation)
		case "RepeatedTest":
			add(evidence.FeatureKotlinRepeatedAnnotation)
		case "TestFactory":
			add(evidence.FeatureKotlinDynamicTestAnnotation)
		}
		if parameterizedSourceAnnotations[name] {
			add(evidence.FeatureKotlinParameterizedSource)
		}
	}
	if bodyStart < 0 || bodyEnd < bodyStart {
		return features
	}
	for i := bodyStart + 1; i < bodyEnd; i++ {
		tok := tokens[i]
		if tok.kind != tokIdent {
			continue
		}
		next := nextSignificant(tokens, i+1)
		if next < 0 || tokens[next].text != "(" {
			continue
		}
		if goldenCallPattern.MatchString(tok.value) {
			add(evidence.FeatureGoldenComparison)
		}
		if snapshotCallPattern.MatchString(tok.value) {
			add(evidence.FeatureSnapshotAssertion)
		}
	}
	return features
}

func nextSignificant(tokens []token, i int) int {
	for i < len(tokens) {
		if tokens[i].kind == tokEOF {
			return -1
		}
		if tokens[i].kind != tokNewline {
			return i
		}
		i++
	}
	return -1
}

func matchingBrace(tokens []token, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch tokens[i].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (p *kotlinParser) skipBalanced(open, close string) {
	depth := 0
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		switch tok.text {
		case open:
			depth++
		case close:
			depth--
			p.pos++
			if depth == 0 {
				return
			}
			continue
		}
		p.pos++
	}
}

func (p *kotlinParser) skipAngle() {
	depth := 0
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		switch tok.text {
		case "<":
			depth++
		case ">":
			depth--
		}
		p.pos++
		if depth == 0 {
			return
		}
	}
}

func mergeFeatures(base, extras []evidence.EvidenceFeature) []evidence.EvidenceFeature {
	merged := append([]evidence.EvidenceFeature(nil), base...)
	seen := make(map[string]bool, len(merged))
	for _, feature := range merged {
		seen[feature.Name] = true
	}
	for _, feature := range extras {
		if seen[feature.Name] {
			continue
		}
		seen[feature.Name] = true
		merged = append(merged, feature)
	}
	return merged
}
