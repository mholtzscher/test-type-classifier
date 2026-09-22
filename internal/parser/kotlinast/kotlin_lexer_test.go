package kotlinast

import "testing"

func tokenTexts(tokens []token) []string {
	texts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		texts = append(texts, tok.text)
	}
	return texts
}

func findToken(tokens []token, kind tokenKind, value string) (token, bool) {
	for _, tok := range tokens {
		if tok.kind == kind && tok.value == value {
			return tok, true
		}
	}
	return token{}, false
}

func TestLexKotlinSkipsNestedBlockAndLineComments(t *testing.T) {
	source := "/* outer /* nested */ comment */\nval x = 1 // trailing comment\nval y = 2\n"
	tokens := lexKotlin(source)
	for _, text := range tokenTexts(tokens) {
		if text == "nested" || text == "comment" || text == "trailing" {
			t.Fatalf("comment text leaked into tokens: %v", tokenTexts(tokens))
		}
	}
	if _, ok := findToken(tokens, tokIdent, "x"); !ok {
		t.Fatal("identifier x missing")
	}
	if _, ok := findToken(tokens, tokIdent, "y"); !ok {
		t.Fatal("identifier y missing")
	}
}

func TestLexKotlinHandlesStringForms(t *testing.T) {
	source := "val s = \"a\\\"b\"\nval raw = \"\"\"line1 \"quoted\"\nline2\"\"\"\nval c = '\\''\nval `weird name` = 1\n"
	tokens := lexKotlin(source)

	regular, ok := findToken(tokens, tokString, "a\\\"b")
	if !ok {
		t.Fatalf("regular string not tokenized correctly: %v", tokenTexts(tokens))
	}
	if regular.text != `"a\"b"` {
		t.Fatalf("regular string raw text = %q", regular.text)
	}

	raw, ok := findToken(tokens, tokString, "line1 \"quoted\"\nline2")
	if !ok {
		t.Fatalf("raw string not tokenized correctly: %v", tokenTexts(tokens))
	}
	if raw.text != `"""line1 "quoted"
line2"""` {
		t.Fatalf("raw string raw text = %q", raw.text)
	}

	if _, ok := findToken(tokens, tokChar, ""); !ok {
		t.Fatal("char literal missing")
	}
	if _, ok := findToken(tokens, tokIdent, "weird name"); !ok {
		t.Fatalf("backtick identifier missing: %v", tokenTexts(tokens))
	}
}

func TestLexKotlinDoesNotPanicOnUnterminatedString(t *testing.T) {
	for _, source := range []string{`val s = "unterminated`, `val s = """unterminated`, `val c = 'x`} {
		_ = lexKotlin(source)
	}
}
