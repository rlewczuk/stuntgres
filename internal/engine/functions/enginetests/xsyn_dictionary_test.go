package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func xsynKernel(t *testing.T, present bool, name string, m, s, k, y bool) *functions.XsynDictionary {
	t.Helper()
	d, err := functions.CompileXsynDictionary(t.Context(), present, name, m, s, k, y)
	if err != nil {
		t.Fatal(err)
	}
	if d.Filtering() || !d.Configured(present, name, m, s, k, y) || d.Configured(!present, name, m, s, k, y) || d.Configured(present, name+"_other", m, s, k, y) || d.Configured(present, name, !m, s, k, y) || d.Configured(present, name, m, !s, k, y) || d.Configured(present, name, m, s, !k, y) || d.Configured(present, name, m, s, k, !y) {
		t.Fatal("immutable resource and Boolean settings", d)
	}
	return d
}

func xsynKernelResult(t *testing.T, d *functions.XsynDictionary, input string, recognized bool, want []fts.Lexeme) {
	t.Helper()
	got, err := d.Lexize(t.Context(), input)
	if err != nil || got.Recognized != recognized || !reflect.DeepEqual(got.Lexemes, want) {
		t.Fatal("rich xsyn result", input, got, want, recognized, err)
	}
}

func TestXsynDictionaryBooleanMatrix(t *testing.T) {
	all := []fts.Lexeme{{Text: "supernova"}, {Text: "sn"}, {Text: "sne"}, {Text: "1987a"}}
	original := []fts.Lexeme{{Text: "supernova"}}
	synonyms := []fts.Lexeme{{Text: "sn"}, {Text: "sne"}, {Text: "1987a"}}
	for _, tc := range []struct {
		m, s, k, y bool
		orig, syn  bool
		want       []fts.Lexeme
	}{
		{false, false, false, false, false, false, nil},
		{false, false, false, true, false, false, nil},
		{false, false, true, false, false, false, nil},
		{false, false, true, true, false, false, nil},
		{false, true, false, false, false, true, nil},
		{false, true, false, true, false, true, synonyms},
		{false, true, true, false, false, true, original},
		{false, true, true, true, false, true, all},
		{true, false, false, false, true, false, nil},
		{true, false, false, true, true, false, synonyms},
		{true, false, true, false, true, false, original},
		{true, false, true, true, true, false, all},
		{true, true, false, false, true, true, nil},
		{true, true, false, true, true, true, synonyms},
		{true, true, true, false, true, true, original},
		{true, true, true, true, true, true, all},
	} {
		d := xsynKernel(t, true, "xsyn_sample", tc.m, tc.s, tc.k, tc.y)
		for _, input := range []string{"supernova", "SUPERNOVA"} {
			var want []fts.Lexeme
			if tc.orig {
				want = tc.want
			}
			xsynKernelResult(t, d, input, tc.orig, want)
		}
		for _, input := range []string{"sn", "SN", "sne", "SNE", "1987a", "1987A"} {
			var want []fts.Lexeme
			if tc.syn {
				want = tc.want
			}
			xsynKernelResult(t, d, input, tc.syn, want)
		}
		for _, input := range []string{"grb", "", " supernova", "supernova ", "supernovax", "#", "Sample", "word", "synonym1"} {
			xsynKernelResult(t, d, input, false, nil)
		}
	}
}

func TestXsynDictionaryResources(t *testing.T) {
	d := xsynKernel(t, false, "", true, false, true, true)
	xsynKernelResult(t, d, "supernova", false, nil)
	// Existing bundled bytes are interpreted by xsyn, not an unaccent
	// matcher or a sample-only name switch. C lowering preserves ©.
	alternate := xsynKernel(t, true, "unaccent", true, false, true, true)
	xsynKernelResult(t, alternate, "©", true, []fts.Lexeme{{Text: "©"}, {Text: "(c)"}})
	for _, tc := range []struct{ name, code, message string }{
		{"", "F0000", `could not open synonym file ".rules": No such file or directory`},
		{"xsyn_missing", "F0000", `could not open synonym file "xsyn_missing.rules": No such file or directory`},
		{"../xsyn_sample", "22023", `invalid text search configuration file name "../xsyn_sample"`},
		{"XSYN_SAMPLE", "22023", `invalid text search configuration file name "XSYN_SAMPLE"`},
		{"xsyn_sample.rules", "22023", `invalid text search configuration file name "xsyn_sample.rules"`},
	} {
		runtime, err := functions.CompileXsynDictionary(t.Context(), true, tc.name, true, false, true, true)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if runtime != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal("resource boundary", tc, runtime, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, present := range []bool{false, true} {
		if runtime, err := functions.CompileXsynDictionary(ctx, present, "xsyn_sample", true, true, true, true); runtime != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled resource compilation published", runtime, err)
		}
	}
}

func TestXsynDictionaryRecognition(t *testing.T) {
	want := []fts.Lexeme{{Text: "supernova"}, {Text: "sn"}, {Text: "sne"}, {Text: "1987a"}}
	d := xsynKernel(t, true, "xsyn_sample", true, true, true, true)
	first, err := d.Lexize(t.Context(), "supernova")
	if err != nil || !first.Recognized || !reflect.DeepEqual(first.Lexemes, want) {
		t.Fatal(first, err)
	}
	second, err := d.Lexize(t.Context(), "sn")
	if err != nil || !second.Recognized || !reflect.DeepEqual(second.Lexemes, want) || &first.Lexemes[0] == &second.Lexemes[0] {
		t.Fatal("invocation ownership", second, err)
	}
	first.Lexemes[0] = fts.Lexeme{Text: "caller mutation", Prefix: true, Variant: 9}
	if !reflect.DeepEqual(second.Lexemes, want) {
		t.Fatal("retained output mutated", second)
	}
	xsynKernelResult(t, d, "sne", true, want)
	empty := xsynKernel(t, true, "xsyn_sample", true, true, false, false)
	xsynKernelResult(t, empty, "supernova", true, nil)
	xsynKernelResult(t, empty, "sn", true, nil)
	xsynKernelResult(t, empty, "grb", false, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, runtime := range []*functions.XsynDictionary{d, empty} {
		for _, input := range []string{"supernova", "grb", ""} {
			got, err := runtime.Lexize(ctx, input)
			if !errors.Is(err, context.Canceled) || got.Recognized || len(got.Lexemes) != 0 {
				t.Fatal("canceled lexization", got, err)
			}
		}
	}
}
