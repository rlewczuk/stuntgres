package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func replacementUnitPlan(t *testing.T, name string, values [4]Value, arity int, conflict bool) StringPlan {
	t.Helper()
	var args [4]Usage
	flags := [4]bool{true, true, true, true}
	for i := range arity {
		args[i] = values[i].Type().Usage()
	}
	text := values[0].Type()
	if text.BaseType().ID() != Text {
		for _, d := range Builtins() {
			if d.ID() == Text {
				text = d
			}
		}
	}
	result := text.Usage()
	if name == "overlay" {
		flags[2], flags[3] = false, false
	}
	if name == "starts_with" {
		for _, d := range Builtins() {
			if d.ID() == Bool {
				result = d.Usage()
			}
		}
	}
	plan, err := BindString(name, result, text.Usage(), args[:arity], flags, conflict)
	if err != nil {
		t.Fatal(name, err)
	}
	return plan
}

func TestStringReplacementAlgorithms(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		source, replacement string
		start, count        int64
		want                string
	}{
		{"Txxxxas", "hom", 2, 4, "Thomas"}, {"abcdef", "XY", 3, 0, "abXYcdef"}, {"abcdef", "XY", 3, -1, "abXYbcdef"},
		{"abcdef", "XY", 2, -4, "aXYabcdef"}, {"abcdef", "X", 9, 1, "abcdefX"}, {"abcdef", "X", 2, 99, "aX"},
		{"", "🙂", 1, 1, "🙂"}, {"abcdef", "", 3, 0, "abcdef"}, {"abcdef", "", 3, 2, "abef"},
		{"Aé🙂Z", "Ω", 2, 1, "AΩ🙂Z"}, {"Aé🙂Z", "Ω🙂", 2, 2, "AΩ🙂Z"}, {"a\u0301b", "X", 2, 1, "aXb"},
		{"abc", "X", math.MaxInt32, 0, "abcX"}, {"abc", "X", 1, math.MinInt32, "Xabc"},
	} {
		got, err := stringOverlay(ctx, tc.source, tc.replacement, tc.start, tc.count)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct{ source, search, replacement, want string }{
		{"abcdefabcdef", "cd", "XX", "abXXefabXXef"}, {"aaaa", "aa", "X", "XX"}, {"aaa", "aa", "X", "Xa"},
		{"aaa", "a", "aa", "aaaaaa"}, {"ababa", "aba", "X", "Xba"}, {"ababa", "a", "", "bb"},
		{"abc", "", "X", "abc"}, {"", "", "X", ""}, {"", "a", "X", ""}, {"abc", "z", "X", "abc"},
		{"é🙂é", "é", "Ω", "Ω🙂Ω"}, {"e\u0301é", "é", "X", "e\u0301X"}, {"a.b", ".", "X", "aXb"},
	} {
		got, err := stringReplace(ctx, tc.source, tc.search, tc.replacement)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct{ source, from, to, want string }{
		{"12345", "143", "ax", "a2x5"}, {"abcabc", "abc", "XY", "XYXY"}, {"abc", "abc", "", ""},
		{"abc", "", "XYZ", "abc"}, {"abc", "a", "XYZ", "Xbc"}, {"", "abc", "XYZ", ""},
		{"abc", "aba", "XYZ", "XYc"}, {"abc", "aba", "X", "Xc"}, {"ab", "ab", "ba", "ba"},
		{"é🙂éZ", "é🙂", "Ω", "ΩΩZ"}, {"abc", "ac", "🙂é", "🙂bé"}, {"a\u0301b", "\u0301", "", "ab"},
	} {
		got, err := stringTranslate(ctx, tc.source, tc.from, tc.to)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		source, prefix string
		want           bool
	}{
		{"alphabet", "alph", true}, {"alphabet", "Alphabet", false}, {"abc", "", true}, {"", "", true},
		{"", "a", false}, {"a", "ab", false}, {"é🙂x", "é🙂", true}, {"e\u0301x", "é", false}, {"e\u0301x", "e", true},
	} {
		work := binarySearchWork{ctx: ctx, remaining: 4096}
		got, err := stringPrefix(tc.source, tc.prefix, &work)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestStringReplacementPlanOwnershipAndStrictness(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	text := types[Text]
	integer := func(n int64) Value {
		v, err := Integer(types[Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		name   string
		arity  int
		values [4]Value
	}{
		{"overlay", 3, [4]Value{String(text, "abc"), String(text, "X"), integer(2)}},
		{"overlay", 4, [4]Value{String(text, "abc"), String(text, "X"), integer(2), integer(1)}},
		{"replace", 3, [4]Value{String(text, "abc"), String(text, "b"), String(text, "X")}},
		{"translate", 3, [4]Value{String(text, "abc"), String(text, "b"), String(text, "X")}},
		{"starts_with", 2, [4]Value{String(text, "abc"), String(text, "a")}},
	} {
		plan := replacementUnitPlan(t, tc.name, tc.values, tc.arity, true)
		id := Text
		if tc.name == "starts_with" {
			id = Bool
		}
		if plan.Result().Type.ID() != id || plan.Result().Typmod != -1 || id == Bool && plan.Result().Collation.ID != 0 {
			t.Fatal(plan.Result())
		}
		for slot := range tc.arity {
			values := tc.values
			values[slot] = Null(values[slot].Type())
			got, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != plan.Result().Type {
				t.Fatal(tc.name, slot, got, err)
			}
		}
		foreign := mathTypes()
		values := tc.values
		values[0] = String(foreign[Text], "abc")
		if _, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("foreign execution accepted", tc.name)
		}
		var args [4]Usage
		flags := [4]bool{true, true, true, true}
		for i := range tc.arity {
			args[i] = tc.values[i].Type().Usage()
		}
		args[0] = foreign[Text].Usage()
		if tc.name == "overlay" {
			flags[2], flags[3] = false, false
		}
		if _, err := BindString(tc.name, plan.Result(), text.Usage(), args[:tc.arity], flags, false); err == nil {
			t.Fatal("foreign formal accepted", tc.name)
		}
	}
	// Converted BPCHAR replacement is trimmed before omitted-count evaluation.
	values := [4]Value{String(text, "abc"), String(types[BPChar], "X  "), integer(2)}
	args := []Usage{text.Usage(), types[BPChar].Usage(), types[Int4].Usage()}
	plan, err := BindString("overlay", text.Usage(), text.Usage(), args, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{})
	s, _ := got.Text()
	physical, _ := values[1].Text()
	if err != nil || s != "aXc" || physical != "X  " || plan.ArgumentUsage(1).Type != text {
		t.Fatal(s, physical, err)
	}
}

func TestStringReplacementBoundsCollationAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		start, count  int64
		code, message string
	}{
		{0, math.MinInt32, "22011", "negative substring length not allowed"},
		{-1, 1, "22011", "negative substring length not allowed"},
		{math.MaxInt32, 1, "22003", "integer out of range"},
	} {
		_, err := stringOverlay(context.Background(), "abc", "X", tc.start, tc.count)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(tc, err)
		}
	}
	long := strings.Repeat("a", 65536)
	for _, f := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := stringOverlay(ctx, long, "X", 40000, 1); return err },
		func(ctx context.Context) error {
			_, err := stringReplace(ctx, long+"b", strings.Repeat("a", 32768)+"c", "X")
			return err
		},
		func(ctx context.Context) error {
			_, err := stringReplace(ctx, strings.Repeat("ab", 32768), "b", long)
			return err
		},
		func(ctx context.Context) error { _, err := stringTranslate(ctx, "x", long+"x", long+"y"); return err },
		func(ctx context.Context) error {
			work := binarySearchWork{ctx: ctx, remaining: 4096}
			_, err := stringPrefix(long, long, &work)
			return err
		},
		func(ctx context.Context) error { var out strings.Builder; return stringWrite(ctx, &out, long) },
	} {
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 2}
		if err := f(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("unbounded replacement work", err)
		}
	}
	for _, f := range []func() error{
		func() error { _, err := stringReplace(context.Background(), long, "z", "X"); return err },
		func() error { _, err := stringReplace(context.Background(), long, "a", "a"); return err },
		func() error { _, err := stringTranslate(context.Background(), long, "z", "X"); return err },
		func() error { _, err := stringOverlay(context.Background(), long, "", 1, 0); return err },
	} {
		if n := testing.AllocsPerRun(10, func() {
			if err := f(); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("identity allocated", n)
		}
	}
}

func TestReplacementPreparedMatcherReuse(t *testing.T) {
	ctx := context.Background()
	for _, needle := range []string{"a", "aa", "aba", "aab", "é🙂"} {
		work := binarySearchWork{ctx: ctx, remaining: 4096}
		matcher, err := prepareBinaryMatcher(needle, &work)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"", "aaaaababa", "é🙂é🙂", "zz" + needle + "zz"} {
			got, err := matcher.position(source, &work)
			if err != nil || got != strings.Index(source, needle) {
				t.Fatal(needle, source, got, err)
			}
			text, err := stringReplace(ctx, source, needle, "X")
			if err != nil || text != strings.ReplaceAll(source, needle, "X") {
				t.Fatal(needle, source, text, err)
			}
		}
	}
}
