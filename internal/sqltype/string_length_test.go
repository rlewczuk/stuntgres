package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStringLengthCountProviders(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id                  ID
		physical            string
		chars, octets, bits int64
	}{
		{Text, "", 0, 0, 0}, {Text, "josé", 4, 5, 40},
		{Text, "é中🙂", 3, 9, 72}, {Text, "é", 2, 3, 24},
		{Text, "é🙂  ", 4, 8, 64}, {VarChar, "é🙂  ", 4, 8, 64},
		{BPChar, "é🙂  ", 2, 8, 48}, {BPChar, "    ", 0, 4, 0},
		{BPChar, "a\t  ", 2, 4, 16}, {BPChar, "a   ", 2, 5, 24},
		{Name, "é🙂", 2, 6, 48}, {Char, "A", 1, 1, 8},
	} {
		for _, name := range []string{"length", "char_length", "character_length", "octet_length", "bit_length"} {
			u := types[tc.id].Usage()
			plan, err := BindString(name, types[Int4].Usage(), types[Text].Usage(), []Usage{u}, [2]bool{true, true}, false)
			if err != nil {
				t.Fatal(tc, name, err)
			}
			target := Text
			if tc.id == BPChar && name != "bit_length" {
				target = BPChar
			}
			if plan.ArgumentUsage(0).Type.ID() != target || plan.Result() != types[Int4].Usage() || plan.Result().Collation.ID != 0 {
				t.Fatal("formal/result authority", tc, name, plan)
			}
			input := String(types[tc.id], tc.physical)
			got, err := plan.Evaluate(ctx, [2]Value{input}, InputEnvironment{}, OutputEnvironment{})
			want := tc.chars
			if name == "octet_length" {
				want = tc.octets
			}
			if name == "bit_length" {
				want = tc.bits
			}
			n, integerErr := got.Integer()
			if err != nil || integerErr != nil || n != want || got.Type() != types[Int4] {
				t.Fatal(tc, name, got, want, err, integerErr)
			}
			physical, _ := input.Text()
			if physical != tc.physical {
				t.Fatal("source mutation", physical)
			}
			got, err = plan.Evaluate(ctx, [2]Value{Null(types[tc.id])}, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Int4] {
				t.Fatal("strict typed NULL", name, got, err)
			}
		}
	}
	base := types[BPChar].Usage()
	base.Typmod = 8 // char(4)
	domain, err := Domain(90001, "fixed", "fixed", base)
	if err != nil {
		t.Fatal(err)
	}
	v, err := domain.Wrap(String(types[BPChar], "é🙂  "))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want int64
	}{{"octet_length", 8}, {"length", 2}, {"bit_length", 48}} {
		p, err := BindString(tc.name, types[Int4].Usage(), types[Text].Usage(), []Usage{domain.Usage()}, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(ctx, [2]Value{v}, InputEnvironment{}, OutputEnvironment{})
		n, _ := got.Integer()
		if err != nil || n != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func naiveStringPosition(source, needle string) int64 {
	s, n := []rune(source), []rune(needle)
	for i := 0; i <= len(s)-len(n); i++ {
		match := true
		for j := range n {
			if s[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return int64(i + 1)
		}
	}
	return 0
}

func TestStringLengthSearchOracle(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	words, level := []string{""}, []string{""}
	for range 4 {
		var next []string
		for _, prefix := range level {
			for _, r := range []rune{'a', 'é', '中', '🙂', '\u0301'} {
				next = append(next, prefix+string(r))
			}
		}
		words = append(words, next...)
		level = next
	}
	for _, name := range []string{"position", "strpos"} {
		p, err := BindString(name, types[Int4].Usage(), text, []Usage{text, text}, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range words {
			for _, needle := range words {
				if len([]rune(needle)) > 2 {
					continue
				}
				got, err := p.Evaluate(context.Background(), [2]Value{String(types[Text], source), String(types[Text], needle)}, InputEnvironment{}, OutputEnvironment{})
				n, _ := got.Integer()
				want := naiveStringPosition(source, needle)
				if err != nil || n != want {
					t.Fatalf("%s source=%q needle=%q got=%d want=%d err=%v", name, source, needle, n, want, err)
				}
			}
		}
		conflict, err := BindString(name, types[Int4].Usage(), text, []Usage{text, text}, [2]bool{true, true}, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, values := range [][2]Value{
			{String(types[Text], "abc"), String(types[Text], "")},
			{String(types[Text], ""), String(types[Text], "")},
		} {
			_, err := conflict.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
			if !errors.As(err, &got) || *got != want {
				t.Fatal(got, err)
			}
		}
		for _, index := range []int{0, 1} {
			values := [2]Value{String(types[Text], ""), String(types[Text], "")}
			values[index] = Null(types[Text])
			got, err := conflict.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Int4] {
				t.Fatal("NULL before collation demand", got, err)
			}
		}
	}
}

func TestStringLengthSignaturesAndOwners(t *testing.T) {
	types, foreign := mathTypes(), mathTypes()
	text, result := types[Text].Usage(), types[Int4].Usage()
	for _, name := range []string{"length", "char_length", "character_length", "octet_length", "bit_length", "position", "strpos"} {
		arity := 1
		if name == "position" || name == "strpos" {
			arity = 2
		}
		for _, args := range [][]Usage{nil, {text, text, text}, {types[Int4].Usage()}, {types[Bytea].Usage()}, {text, types[Bytea].Usage()}} {
			if _, err := BindString(name, result, text, args, [2]bool{true, true}, false); err == nil {
				t.Fatal("invalid signature", name, args)
			}
		}
		args := []Usage{text, text}[:arity]
		for _, wrong := range []Usage{types[Text].Usage(), types[Int8].Usage(), {Type: types[Int4], Typmod: 3}} {
			if _, err := BindString(name, wrong, text, args, [2]bool{true, true}, false); err == nil {
				t.Fatal("invalid result", name, wrong)
			}
		}
		if _, err := BindString(name, result, text, args, [2]bool{}, false); err == nil {
			t.Fatal("mixed input", name)
		}
		if _, err := BindString(name, result, text, []Usage{foreign[Text].Usage(), text}[:arity], [2]bool{true, true}, false); err == nil {
			t.Fatal("foreign TEXT formal", name)
		}
		p, err := BindString(name, result, text, args, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(context.Background(), [2]Value{String(foreign[Text], "x"), String(types[Text], "x")}, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("foreign execution owner", name)
		}
	}
}

func TestStringLengthOverflowCancellationAndAllocations(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		count      int64
		bits, fail bool
		want       int64
	}{
		{math.MaxInt32 / 8, true, false, 2147483640}, {math.MaxInt32/8 + 1, true, true, 0},
		{math.MaxInt32, false, false, math.MaxInt32}, {math.MaxInt32 + 1, false, true, 0},
	} {
		v, err := integerLength(types[Int4], tc.count, tc.bits)
		if tc.fail {
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"}
			if !errors.As(err, &got) || *got != want {
				t.Fatal(got, err)
			}
		} else {
			n, _ := v.Integer()
			if err != nil || n != tc.want {
				t.Fatal(v, err)
			}
		}
	}
	long := strings.Repeat("é🙂", 8192)
	for _, f := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := stringCharacters(ctx, long); return err },
		func(ctx context.Context) error {
			_, err := trimStringPadding(ctx, strings.Repeat(" ", 65536))
			return err
		},
		func(ctx context.Context) error { _, err := stringPosition(ctx, long+"z", "z"); return err },
	} {
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 2}
		if err := f(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("bounded cancellation", err)
		}
	}
	for _, name := range []string{"length", "char_length", "character_length", "octet_length", "bit_length", "position", "strpos"} {
		args := []Usage{types[BPChar].Usage()}
		values := [2]Value{String(types[BPChar], long+"  ")}
		if name == "position" || name == "strpos" {
			args = append(args, types[Text].Usage())
			values[1] = String(types[Text], "🙂")
		}
		p, err := BindString(name, types[Int4].Usage(), types[Text].Usage(), args, [2]bool{true, true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if allocs := testing.AllocsPerRun(10, func() {
			if _, err := p.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{}); err != nil {
				panic(err)
			}
		}); allocs != 0 {
			t.Fatal("payload scratch allocation", name, allocs)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	// A one-byte needle and short source need no matcher work checkpoint.
	// Entry and found checks succeed; cancellation is at the prefix scan.
	prefix := &mathCheckpointContext{Context: context.Background(), remaining: 3}
	if _, err := stringPosition(prefix, strings.Repeat("a", 1024)+"z", "z"); !errors.Is(err, context.Canceled) {
		t.Fatal("prefix-count cancellation", err)
	}
}
