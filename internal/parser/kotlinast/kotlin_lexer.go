package kotlinast

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenKind classifies lexer output. The parser only needs enough grammar to
// find annotations, declarations, braces, and imports, so the token set stays
// intentionally small.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent
	tokString
	tokChar
	tokNumber
	tokPunct
)

type token struct {
	kind  tokenKind
	text  string // raw source text
	value string // decoded identifier or string content
	off   int    // byte offset of the first byte
	end   int    // byte offset one past the last byte
}

// lexKotlin scans Kotlin source into tokens, correctly skipping line comments,
// nested block comments, regular and raw strings, and character literals. The
// scanner never needs symbol resolution and never panics on malformed input; it
// stops at the first unterminated construct.
func lexKotlin(source string) []token {
	var tokens []token
	i := 0
	for i < len(source) {
		c := source[i]
		switch {
		case c == '\n':
			tokens = append(tokens, token{kind: tokNewline, text: "\n", off: i, end: i + 1})
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(source) && source[i+1] == '*':
			i = skipBlockComment(source, i)
		case c == '"':
			tok, next := lexString(source, i)
			tokens = append(tokens, tok)
			i = next
		case c == '\'':
			tok, next := lexChar(source, i)
			tokens = append(tokens, tok)
			i = next
		case c == '`':
			tok, next := lexBacktickIdent(source, i)
			tokens = append(tokens, tok)
			i = next
		case isIdentStart(source, i):
			start := i
			for i < len(source) && isIdentPart(source, i) {
				i++
			}
			text := source[start:i]
			tokens = append(tokens, token{kind: tokIdent, text: text, value: text, off: start, end: i})
		case c >= '0' && c <= '9':
			start := i
			for i < len(source) && (isIdentPart(source, i) || source[i] == '.') {
				i++
			}
			tokens = append(tokens, token{kind: tokNumber, text: source[start:i], off: start, end: i})
		default:
			_, size := utf8.DecodeRuneInString(source[i:])
			if size == 0 {
				size = 1
			}
			tokens = append(tokens, token{kind: tokPunct, text: source[i : i+size], off: i, end: i + size})
			i += size
		}
	}
	tokens = append(tokens, token{kind: tokEOF, off: len(source), end: len(source)})
	return tokens
}

func skipBlockComment(source string, i int) int {
	depth := 0
	for i < len(source) {
		switch {
		case strings.HasPrefix(source[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(source[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			_, size := utf8.DecodeRuneInString(source[i:])
			if size == 0 {
				size = 1
			}
			i += size
		}
	}
	return i
}

func lexString(source string, i int) (token, int) {
	if strings.HasPrefix(source[i:], `"""`) {
		start := i
		i += 3
		for i < len(source) {
			if strings.HasPrefix(source[i:], `"""`) {
				i += 3
				return token{kind: tokString, text: source[start:i], value: source[start+3 : i-3], off: start, end: i}, i
			}
			_, size := utf8.DecodeRuneInString(source[i:])
			if size == 0 {
				size = 1
			}
			i += size
		}
		return token{kind: tokString, text: source[start:], value: source[start+3:], off: start, end: len(source)}, len(source)
	}
	start := i
	i++
	for i < len(source) {
		switch source[i] {
		case '\\':
			i += 2
			continue
		case '"':
			i++
			return token{kind: tokString, text: source[start:i], value: source[start+1 : i-1], off: start, end: i}, i
		case '\n':
			return token{kind: tokString, text: source[start:i], value: source[start+1 : i], off: start, end: i}, i
		}
		i++
	}
	return token{kind: tokString, text: source[start:], value: source[start+1:], off: start, end: len(source)}, len(source)
}

func lexChar(source string, i int) (token, int) {
	start := i
	i++
	for i < len(source) {
		switch source[i] {
		case '\\':
			i += 2
			continue
		case '\'':
			i++
			return token{kind: tokChar, text: source[start:i], off: start, end: i}, i
		case '\n':
			return token{kind: tokChar, text: source[start:i], off: start, end: i}, i
		}
		i++
	}
	return token{kind: tokChar, text: source[start:], off: start, end: len(source)}, len(source)
}

func lexBacktickIdent(source string, i int) (token, int) {
	start := i
	i++
	for i < len(source) && source[i] != '`' && source[i] != '\n' {
		_, size := utf8.DecodeRuneInString(source[i:])
		if size == 0 {
			size = 1
		}
		i += size
	}
	if i < len(source) && source[i] == '`' {
		i++
	}
	return token{kind: tokIdent, text: source[start:i], value: source[start+1 : i-1], off: start, end: i}, i
}

func isIdentStart(source string, i int) bool {
	c := source[i]
	if c == '_' {
		return true
	}
	if c >= utf8.RuneSelf {
		r, _ := utf8.DecodeRuneInString(source[i:])
		return unicode.IsLetter(r)
	}
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(source string, i int) bool {
	c := source[i]
	if c == '_' || c >= '0' && c <= '9' {
		return true
	}
	if c >= utf8.RuneSelf {
		r, _ := utf8.DecodeRuneInString(source[i:])
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
