package functions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
)

func synonymCompiled(t *testing.T, data string, sensitive bool) *SynonymDictionary {
	t.Helper()
	d, err := loadSynonymDictionary(context.Background(), fstest.MapFS{"fixture.syn": &fstest.MapFile{Data: []byte(data)}}, "fixture", sensitive)
	if err != nil || d == nil || !d.Configured("fixture", sensitive) || d.Filtering() {
		t.Fatal(d, err)
	}
	return d
}

func synonymLexeme(t *testing.T, d *SynonymDictionary, input string, expected *fts.Lexeme) {
	t.Helper()
	got, err := d.Lexize(context.Background(), input)
	want := fts.LexicalResult{}
	if expected != nil {
		want = fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{*expected}}
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q: %+v %v want %+v", input, got, err, want)
	}
}

func TestSynonymParsing(t *testing.T) {
	d := synonymCompiled(t, "\n  \t\r\nlonely\nlonely_spaces \t\r\n  a\tb c\nb second\n# heading\n\"source\" \"target\" extra\nvertical\vvalue\nform\fvalue\ncarriage\rvalue\nnbsp\u00a0key result\n", false)
	for _, tc := range [][2]string{{"a", "b"}, {"b", "second"}, {"#", "heading"}, {`"source"`, `"target"`}, {"vertical", "value"}, {"form", "value"}, {"carriage", "value"}, {"nbsp\u00a0key", "result"}} {
		synonymLexeme(t, d, tc[0], &fts.Lexeme{Text: tc[1]})
	}
	for _, input := range []string{"", "lonely", "lonely_spaces", " a", "a ", "a b", "heading", "source", "nbsp"} {
		synonymLexeme(t, d, input, nil)
	}
	for _, data := range []string{"", "\n \t\r\n", "one", "one \t"} {
		empty := synonymCompiled(t, data, false)
		synonymLexeme(t, empty, "one", nil)
		synonymLexeme(t, empty, "", nil)
	}
	duplicate := synonymCompiled(t, "key first\nkey second\n", false)
	got, err := duplicate.Lexize(context.Background(), "KEY")
	if err != nil || !got.Recognized || len(got.Lexemes) != 1 || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 || !slices.Contains([]string{"first", "second"}, got.Lexemes[0].Text) {
		t.Fatal("duplicate membership", got, err)
	}
	// The table really is sorted for lookup, including nontrivial heap shapes.
	for size := range 40 {
		var definitions strings.Builder
		for i := size - 1; i >= 0; i-- {
			fmt.Fprintf(&definitions, "k%02d v%02d\n", i, i)
		}
		ordered := synonymCompiled(t, definitions.String(), true)
		for i := range size {
			synonymLexeme(t, ordered, fmt.Sprintf("k%02d", i), &fts.Lexeme{Text: fmt.Sprintf("v%02d", i)})
		}
	}
}

func TestSynonymCase(t *testing.T) {
	data := "Paris PARIS\nÄBC ÄVALUE\n"
	insensitive, sensitive := synonymCompiled(t, data, false), synonymCompiled(t, data, true)
	for _, input := range []string{"Paris", "PARIS", "paris"} {
		synonymLexeme(t, insensitive, input, &fts.Lexeme{Text: "paris"})
	}
	synonymLexeme(t, sensitive, "Paris", &fts.Lexeme{Text: "PARIS"})
	synonymLexeme(t, sensitive, "PARIS", nil)
	synonymLexeme(t, insensitive, "Äbc", &fts.Lexeme{Text: "Ävalue"})
	synonymLexeme(t, insensitive, "äbc", nil)
	synonymLexeme(t, sensitive, "ÄBC", &fts.Lexeme{Text: "ÄVALUE"})
	synonymLexeme(t, sensitive, "Äbc", nil)
	if sensitive.Configured("different", true) || sensitive.Configured("fixture", false) {
		t.Fatal("configuration identity ignored")
	}
}

func TestSynonymPrefix(t *testing.T) {
	d := synonymCompiled(t, "indices index*\nword value**\nsource* internal*star\nsole *\n", false)
	for _, tc := range []struct {
		input, text string
		prefix      bool
	}{{"indices", "index", true}, {"word", "value*", true}, {"source*", "internal*star", false}, {"sole", "", true}} {
		synonymLexeme(t, d, tc.input, &fts.Lexeme{Text: tc.text, Prefix: tc.prefix})
	}
	synonymLexeme(t, d, "source", nil)
	first, err := d.Lexize(context.Background(), "indices")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Lexize(context.Background(), "indices")
	if err != nil || &first.Lexemes[0] == &second.Lexemes[0] {
		t.Fatal("shared result backing", err)
	}
	first.Lexemes[0] = fts.Lexeme{Text: "mutated", Variant: 3}
	if second.Lexemes[0] != (fts.Lexeme{Text: "index", Prefix: true}) {
		t.Fatal("result ownership", second)
	}
	synonymLexeme(t, d, "indices", &fts.Lexeme{Text: "index", Prefix: true})
	stock, err := CompileSynonymDictionary(context.Background(), "synonym_sample", false)
	if err != nil {
		t.Fatal(err)
	}
	synonymLexeme(t, stock, "indices", &fts.Lexeme{Text: "index", Prefix: true})
}

func TestSynonymResourceDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, code, message string }{
		{"synonym_missing", "F0000", `could not open synonym file "synonym_missing.syn": No such file or directory`},
		{"", "F0000", `could not open synonym file ".syn": No such file or directory`},
		{"../synonym_sample", "22023", `invalid text search configuration file name "../synonym_sample"`},
	} {
		d, err := CompileSynonymDictionary(context.Background(), tc.name, false)
		var actual *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if d != nil || !errors.As(err, &actual) || *actual != want {
			t.Fatal(d, err, want)
		}
	}
	for _, tc := range []struct{ raw, message string }{
		{"valid word\ninvalid \xff\n", `invalid byte sequence for encoding "UTF8": 0xff`},
		{"valid word\ninvalid \xe2\x82", `invalid byte sequence for encoding "UTF8": 0xe2 0x82`},
	} {
		d, err := loadSynonymDictionary(context.Background(), fstest.MapFS{"broken.syn": &fstest.MapFile{Data: []byte(tc.raw)}}, "broken", false)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: tc.message, Context: `line 2 of configuration file "broken.syn"`}
		var actual *sqlstate.Error
		if d != nil || !errors.As(err, &actual) || *actual != want {
			t.Fatal("real byte decode diagnostic", d, err, want)
		}
	}
}

func TestSynonymCancellation(t *testing.T) {
	original := context.Background()
	data := strings.Repeat("LongSource LongReplacement* extra\n", 256)
	for _, phase := range []string{"parse", "load"} {
		t.Run(phase, func(t *testing.T) {
			compile := func(ctx context.Context) (*SynonymDictionary, error) {
				if phase == "load" {
					return loadSynonymDictionary(ctx, fstest.MapFS{"fixture.syn": &fstest.MapFile{Data: []byte(data)}}, "fixture", false)
				}
				return compileSynonymDictionary(ctx, data, "fixture", false)
			}
			calibration := &aclValuesScanContext{Context: original}
			if d, err := compile(calibration); err != nil || d == nil {
				t.Fatal(d, err)
			}
			ctx, cancel := context.WithCancel(original)
			defer cancel()
			progress := &aclValuesScanContext{Context: ctx, cancel: cancel, after: max(2, calibration.calls/4)}
			if d, err := compile(progress); d != nil || !errors.Is(err, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("in-progress compilation published", d, err)
			}
		})
	}
	entries := make([]synonymEntry, 512)
	for i := range entries {
		entries[i].input = fmt.Sprintf("k%04d", len(entries)-i)
	}
	calibration := &aclValuesScanContext{Context: original}
	if err := sortSynonyms(calibration, slices.Clone(entries)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(original)
	defer cancel()
	progress := &aclValuesScanContext{Context: ctx, cancel: cancel, after: max(2, calibration.calls/2)}
	if err := sortSynonyms(progress, entries); !errors.Is(err, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("sort did not stop", err)
	}
	d := synonymCompiled(t, "word value\n", false)
	canceled, stop := context.WithCancel(original)
	stop()
	if result, err := d.Lexize(canceled, "WORD"); result.Recognized || len(result.Lexemes) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(result, err)
	}
	if compiled, err := CompileSynonymDictionary(canceled, "synonym_sample", false); compiled != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(compiled, err)
	}
	ctx2, cancel2 := context.WithCancel(original)
	defer cancel2()
	lowering := &aclValuesScanContext{Context: ctx2, cancel: cancel2, after: 3}
	if result, err := d.Lexize(lowering, strings.Repeat("WORD", 2048)); result.Recognized || len(result.Lexemes) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("lookup lowering interruption", result, err)
	}
	synonymLexeme(t, d, "WORD", &fts.Lexeme{Text: "value"})
}
