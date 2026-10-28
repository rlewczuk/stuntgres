package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func dictionaryArrayType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == 1009 {
			return d
		}
	}
	t.Fatal("TEXT[] missing")
	return nil
}
func assertLexeme(t *testing.T, d *SimpleDictionary, input string, want []string, null bool) {
	t.Helper()
	array := dictionaryArrayType(t)
	lexical, err := d.Lexize(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if lexical.Recognized == null {
		t.Fatal("recognition", lexical)
	}
	for _, word := range lexical.Lexemes {
		if word.Prefix || word.Variant != 0 {
			t.Fatal("ordinary dictionary flags", word)
		}
	}
	v, err := sqltype.LexicalArray(array, lexical)
	if err != nil || v.Type() != array || v.IsNull() != null {
		t.Fatal("typed lexize", v, err)
	}
	if null {
		return
	}
	if v.ArrayLen() != len(want) {
		t.Fatal("array length", v.ArrayLen(), want)
	}
	for i, text := range want {
		element := v.ArrayElement(i)
		got, err := element.Text()
		if err != nil || got != text || element.Type() != array.Element() {
			t.Fatal("typed lexeme", got, text, err)
		}
	}
	if len(want) == 0 && len(v.ArrayDimensions()) != 0 {
		t.Fatal("empty array has dimensions")
	}
}
func TestSimpleDictionaryNormalization(t *testing.T) {
	d, err := CompileSimpleDictionary(context.Background(), true, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{{"WORD", "word"}, {"Cats!", "cats!"}, {"A B", "a b"}, {"ÄBC", "Äbc"}, {" A ", " a "}, {`A,B"C\D{ E}`, `a,b"c\d{ e}`}} {
		assertLexeme(t, d, tc.input, []string{tc.want}, false)
	}
	assertLexeme(t, d, "", nil, false)
}
func TestSimpleDictionaryStopwords(t *testing.T) {
	d, err := CompileSimpleDictionary(context.Background(), true, "english")
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"THE", "S", "T", "DON", "MYSELF", "YOURSELVES", "NOW"} {
		assertLexeme(t, d, word, nil, false)
	}
	assertLexeme(t, d, "YeS", []string{"yes"}, false)
	filter, err := CompileSimpleDictionary(context.Background(), false, "english")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.words) != 127 || &d.words[0] != &filter.words[0] {
		t.Fatal("full released list must be shared", len(d.words))
	}
	assertLexeme(t, filter, "YeS", nil, true)
	assertLexeme(t, filter, "The", nil, false)
	assertLexeme(t, filter, "", nil, false)
	got, err := compileStopwords(context.Background(), "WORD suffix\n Leading\n\tLeading\n#notacomment\rignored\n\nÄBC\n")
	if err != nil || !reflect.DeepEqual(got, []string{"#notacomment", "word", "Äbc"}) {
		t.Fatal("C whitespace prefixes", got, err)
	}
	for _, tc := range []struct{ name, code, message string }{{"missing_dictionary", "F0000", `could not open stop-word file "missing_dictionary.stop": No such file or directory`}, {"../english", "22023", `invalid text search configuration file name "../english"`}, {"English", "22023", `invalid text search configuration file name "English"`}} {
		compiled, err := CompileSimpleDictionary(context.Background(), true, tc.name)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if compiled != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("%s: state=%v diagnostic=%#v, want %#v", tc.name, compiled, diagnostic, want)
		}
	}
}
func TestSimpleDictionaryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, err := CompileSimpleDictionary(ctx, true, "english"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	d, err := CompileSimpleDictionary(context.Background(), true, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := d.Lexize(ctx, strings.Repeat("WORD", 1024)); v.Recognized || len(v.Lexemes) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(v, err)
	}
	if words, err := compileStopwords(ctx, "word\n"); words != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(words, err)
	}
}
