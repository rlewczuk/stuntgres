package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"sync"
	"testing"
)

func TestStringCaseProviderAuthority(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	for _, name := range []string{"lower", "upper", "casefold", "initcap"} {
		for _, id := range []uint32{100, 950, 951, 811, 6411} {
			result := text
			result.Collation.ID = id
			if !result.Valid() {
				t.Fatal("case collation usage invalid", id)
			}
			plan, err := BindString(name, result, text, []Usage{result}, [2]bool{true}, true)
			if err != nil || plan.Result() != result || !plan.CollationIndeterminate() {
				t.Fatal("retained authority", plan, err)
			}
			got, err := plan.Evaluate(context.Background(), [2]Value{Null(types[Text])}, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Text] {
				t.Fatal("strict typed NULL before classification", got, err)
			}
			_, err = plan.Evaluate(context.Background(), [2]Value{String(types[Text], "")}, InputEnvironment{}, OutputEnvironment{})
			messageName := name
			if name == "casefold" {
				messageName = "lower"
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for " + messageName + "() function", Hint: "Use the COLLATE clause to set the collation explicitly."}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("full collation diagnostic", err)
			}
			if _, err := plan.Evaluate(context.Background(), [2]Value{Null(types[Int4])}, InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign typed NULL accepted")
			}
		}
		for _, args := range [][]Usage{nil, {text, text}, {types[Int4].Usage()}, {types[Bool].Usage()}, {types[Bytea].Usage()}} {
			if _, err := BindString(name, text, text, args, [2]bool{true, true}, false); err == nil {
				t.Fatal("invalid case signature", name, args)
			}
		}
	}
}

func TestStringCaseProviderCharacterConversion(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	for _, tc := range []struct {
		id          ID
		input, want string
	}{
		{Text, "AbC  ", "abc  "}, {VarChar, "AbC  ", "abc  "}, {BPChar, "AbC  ", "abc"}, {Name, "AbC", "abc"}, {Char, "A", "a"},
	} {
		arg := types[tc.id].Usage()
		plan, err := BindString("lower", text, text, []Usage{arg}, [2]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		input, err := types[tc.id].Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Evaluate(context.Background(), [2]Value{input}, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := got.Text()
		if err != nil || s != tc.want || got.Type() != types[Text] || plan.Result().Typmod != -1 {
			t.Fatal("case character conversion", tc, s, err)
		}
	}
	arg := types[VarChar].Usage()
	arg.Typmod = 5
	result := text
	result.Collation.ID = 6411
	plan, err := BindString("upper", result, text, []Usage{arg}, [2]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Evaluate(context.Background(), [2]Value{String(types[VarChar], "ß")}, InputEnvironment{}, OutputEnvironment{})
	s, textErr := got.Text()
	if err != nil || textErr != nil || s != "SS" || plan.Result() != result {
		t.Fatal("TEXT expansion without source width", s, err, textErr)
	}
}

func TestStringCaseProviderReleaseSigmaPrefix(t *testing.T) {
	text := mathTypes()[Text].Usage()
	full := text
	full.Collation.ID = 6411
	for _, op := range []string{"lower", "initcap"} {
		plan, err := BindString(op, full, text, []Usage{full}, [2]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ input, lower, initcap string }{
			{"\u0345Σ", "\u0345ς", "Ις"}, {"\u0301Σ", "\u0301ς", "\u0301Σ"},
			{"\u0301\u0345Σ", "\u0301\u0345ς", "\u0301Ις"},
			{"Σ", "σ", "Σ"}, {"1\u0345Σ", "1\u0345σ", "1\u0345σ"},
			{"\u0345ΣΑ", "\u0345σα", "Ισα"}, {"İΣ", "i\u0307ς", "İς"},
			{"", "", ""}, {"\u0345Σ", "\u0345ς", "Ις"},
		} {
			want := tc.lower
			if op == "initcap" {
				want = tc.initcap
			}
			got, err := plan.Evaluate(context.Background(), [2]Value{String(text.Type, tc.input)}, InputEnvironment{}, OutputEnvironment{})
			s, textErr := got.Text()
			if err != nil || textErr != nil || s != want || got.Type() != text.Type {
				t.Fatalf("%s original-input prefix %q: %q want %q errors=%v/%v", op, tc.input, s, want, err, textErr)
			}
		}
	}
}

func TestStringCaseProviderImmutableExecutions(t *testing.T) {
	text := mathTypes()[Text].Usage()
	full := text
	full.Collation.ID = 6411
	plan, err := BindString("casefold", full, text, []Usage{text}, [2]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	before := plan
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for _, tc := range []struct{ input, want string }{{"Straße", "strasse"}, {"İ", "i\u0307"}, {"ΑΣ", "ασ"}, {"", ""}, {"ﬃ", "ffi"}} {
				got, err := plan.Evaluate(context.Background(), [2]Value{String(text.Type, tc.input)}, InputEnvironment{}, OutputEnvironment{})
				s, textErr := got.Text()
				if err != nil || textErr != nil || s != tc.want {
					t.Errorf("fresh execution %q: %q %v %v", tc.input, s, err, textErr)
				}
			}
		})
	}
	workers.Wait()
	if plan != before {
		t.Fatal("execution mutated retained plan")
	}
}
