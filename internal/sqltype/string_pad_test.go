package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestStringPadAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		source, fill string
		n            int64
		left, right  string
	}{
		{"hi", "xy", 5, "xyxhi", "hixyx"}, {"abcdef", "xy", 3, "abc", "abc"},
		{"é🙂", "界x", 5, "界x界é🙂", "é🙂界x界"}, {"é🙂", "x", 1, "é", "é"},
		{"e\u0301", "x", 1, "e", "e"}, {"", "xy", 3, "xyx", "xyx"},
		{"abc", "", 5, "abc", "abc"}, {"abc", "", 2, "ab", "ab"},
		{"abc", "", math.MaxInt32, "abc", "abc"}, {"abc", "x", 0, "", ""},
		{"abc", "x", -1, "", ""}, {"abc", "x", math.MinInt32, "", ""},
	} {
		for _, left := range []bool{false, true} {
			want := tc.right
			if left {
				want = tc.left
			}
			got, err := stringPad(context.Background(), tc.source, tc.fill, tc.n, left)
			if err != nil || got != want {
				t.Fatal(tc, left, got, err)
			}
		}
	}
}

func TestStringRepeatAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int64
		want string
	}{
		{"Pg", 4, "PgPgPgPg"}, {"é🙂", 2, "é🙂é🙂"}, {" x ", 2, " x  x "},
		{"", math.MaxInt32, ""}, {"abc", 1, "abc"}, {"abc", 0, ""}, {"abc", -1, ""}, {"abc", math.MinInt32, ""},
	} {
		got, err := stringRepeat(context.Background(), tc.s, tc.n)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestStringReverseAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		s, want string
		binary  bool
	}{
		{"", "", false}, {"🙂", "🙂", false}, {"Aé🙂Z", "Z🙂éA", false},
		{"a\u0301b", "b\u0301a", false}, {"a\u200db", "b\u200da", false},
		{string([]byte{0, 0xff, 0x80, 0x41}), string([]byte{0x41, 0x80, 0xff, 0}), true},
		{string([]byte{0xc3, 0xa9}), string([]byte{0xa9, 0xc3}), true},
	} {
		got, err := stringReverse(context.Background(), tc.s, tc.binary)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		again, err := stringReverse(context.Background(), got, tc.binary)
		if err != nil || again != tc.s {
			t.Fatal(tc, again, err)
		}
	}
}

func TestStringPadSizeGuards(t *testing.T) {
	for _, tc := range []struct {
		width, n int64
		fail     bool
	}{
		{4, 268435454, false}, {4, 268435455, true}, {1, 1073741819, false}, {1, 1073741820, true},
		{0, math.MaxInt64, false}, {math.MaxInt64, 2, true},
	} {
		err := stringPadSize(tc.width, tc.n)
		if !tc.fail {
			if err != nil {
				t.Fatal(tc, err)
			}
			continue
		}
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "requested length too large"}
		if !errors.As(err, &d) || *d != want {
			t.Fatal(tc, err)
		}
	}
	if _, err := stringPad(context.Background(), "x", "x", 268435455, true); err == nil {
		t.Fatal("padding missed UTF8 worst-case guard")
	}
	if _, err := stringRepeat(context.Background(), "x", 1073741820); err == nil {
		t.Fatal("repeat missed varlena guard")
	}
}

func TestStringPadPlans(t *testing.T) {
	types := mathTypes()
	text, integer := types[Text], types[Int4]
	for _, tc := range []struct {
		name  string
		arity int
		want  string
	}{{"lpad", 2, "  a"}, {"lpad", 3, "xxa"}, {"rpad", 2, "a  "}, {"rpad", 3, "axx"}, {"repeat", 2, "aaa"}, {"reverse", 1, "a"}} {
		args := []Usage{text.Usage(), integer.Usage(), text.Usage()}
		flags := [4]bool{true, false, true}
		values := [4]Value{String(text, "a"), {}, String(text, "x")}
		values[1], _ = Integer(integer, 3)
		plan, err := BindString(tc.name, text.Usage(), text.Usage(), args[:tc.arity], flags, true)
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		s, err := got.Text()
		if err != nil || s != tc.want {
			t.Fatal(tc, s, err)
		}
		for i := range tc.arity {
			nulls := values
			nulls[i] = Null(args[i].Type)
			got, err := plan.Evaluate(context.Background(), nulls, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != text {
				t.Fatal(tc, i, got, err)
			}
		}
		foreign := values
		foreign[0] = String(mathTypes()[Text], "a")
		if _, err := plan.Evaluate(context.Background(), foreign, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("foreign owner accepted")
		}
		if _, err := BindString(tc.name, types[Bool].Usage(), text.Usage(), args[:tc.arity], flags, false); err == nil {
			t.Fatal("wrong result accepted")
		}
		if tc.arity > 1 {
			values[1], _ = Integer(integer, math.MaxInt32)
			_, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "54000" {
				t.Fatal(tc, err)
			}
			want := ""
			if tc.name == "lpad" || tc.name == "rpad" {
				if tc.arity == 2 {
					want = "SQL function \"" + tc.name + "\" statement 1"
				}
			}
			if d.Context != want {
				t.Fatal(tc, d)
			}
		}
	}
	for _, name := range []string{"lpad", "rpad", "repeat"} {
		if _, err := BindString(name, text.Usage(), text.Usage(), []Usage{text.Usage(), types[Int8].Usage()}, [4]bool{true}, false); err == nil {
			t.Fatal("narrowed int8 provider", name)
		}
	}
}

func TestBinaryReversePlan(t *testing.T) {
	types := mathTypes()
	d := types[Bytea]
	plan, err := BindBinary("reverse", d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err := Binary(d, string([]byte{0, 0xff, 0x80}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Evaluate(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := got.Binary()
	if err != nil || raw != string([]byte{0x80, 0xff, 0}) {
		t.Fatal(got, err)
	}
	got, err = plan.Evaluate(context.Background(), Null(d))
	if err != nil || !got.IsNull() || got.Type() != d {
		t.Fatal(got, err)
	}
	if _, err := BindBinary("reverse", types[Int4].Usage(), d.Usage()); err == nil {
		t.Fatal("integer reverse result accepted")
	}
	if _, err := BindBinary("reverse", d.Usage(), d.Usage(), d.Usage()); err == nil {
		t.Fatal("wrong arity accepted")
	}
	foreign, err := Binary(mathTypes()[Bytea], "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Evaluate(context.Background(), foreign); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestStringPadCancellationAndIdentity(t *testing.T) {
	long := strings.Repeat("é", 32768)
	for _, f := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := stringPad(ctx, long, "x", 40000, true); return err },
		func(ctx context.Context) error { _, err := stringPad(ctx, "a", "x", 65536, false); return err },
		func(ctx context.Context) error { _, err := stringRepeat(ctx, "a", 65536); return err },
		func(ctx context.Context) error { _, err := stringReverse(ctx, long, false); return err },
		func(ctx context.Context) error { _, err := stringReverse(ctx, long, true); return err },
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := f(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := f(&mathCheckpointContext{Context: context.Background(), remaining: 2}); !errors.Is(err, context.Canceled) {
			t.Fatal("unbounded work", err)
		}
	}
	for _, f := range []func() (string, error){
		func() (string, error) { return stringPad(context.Background(), "abc", "", math.MaxInt32, true) },
		func() (string, error) { return stringRepeat(context.Background(), long, 1) },
		func() (string, error) { return stringReverse(context.Background(), "🙂", false) },
	} {
		if n := testing.AllocsPerRun(10, func() {
			if _, err := f(); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("identity allocated", n)
		}
	}
}
