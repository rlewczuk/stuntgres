package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNameCodec(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Name]
	u := d.Usage()
	if d.ID() != 19 || d.Name() != "name" || d.DatabaseName() != "NAME" || d.SQLName() != "NAME" || d.DiagnosticName() != "name" || !u.Valid() || u.Typmod != -1 || u.Collation != (CollationRef{ID: 950, Derivation: Default}) {
		t.Fatal(d, u)
	}
	if IsCharacter(Name) {
		t.Fatal("NAME must not inherit character typmods or metadata")
	}
	if _, ok := u.Length(); ok {
		t.Fatal("NAME length exposed")
	}
	if declaration, err := u.Declaration(); err != nil || declaration != "NAME" {
		t.Fatal(declaration, err)
	}
	for _, tc := range []struct{ in, want string }{
		{"MiXeD", "MiXeD"}, {"", ""}, {" x \t", " x \t"}, {"O'Brien\\tail\n", "O'Brien\\tail\n"},
		{strings.Repeat("a", 62), strings.Repeat("a", 62)}, {strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 100), strings.Repeat("a", 63)},
		{strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
		{strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)}, {strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"},
		{strings.Repeat("é", 32), strings.Repeat("é", 31)}, {strings.Repeat("a", 62) + "e\u0301", strings.Repeat("a", 62) + "e"},
	} {
		v, err := d.Input(ctx, tc.in, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want || v.Type() != d {
			t.Fatal(tc, out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+strings.ReplaceAll(tc.want, "'", "''")+"'" {
			t.Fatal(literal, err)
		}
	}
	for _, prefix := range []string{"", strings.Repeat("a", 63)} {
		for _, tc := range []struct{ in, suffix string }{{"\x00", "0x00"}, {"\xff", "0xff"}, {"\xc3", "0xc3"}, {"\xc3(", "0xc3 0x28"}} {
			_, err := d.Input(ctx, prefix+tc.in, -1)
			numericProviderError(t, err, "22021", `invalid byte sequence for encoding "UTF8": `+tc.suffix)
		}
	}
	_, _, err := d.ResolveModifiers(3)
	numericProviderError(t, err, "42601", `type modifier is not allowed for type "name"`)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = d.Input(ctx, "x", -1)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNameCoercion(t *testing.T) {
	ctx := context.Background()
	ds := testTypes()
	for _, tc := range []struct {
		source, target ID
		mode           CoercionContext
		in, want       string
	}{
		{Text, Name, Implicit, strings.Repeat("x", 64), strings.Repeat("x", 63)},
		{VarChar, Name, Implicit, " a  ", " a  "},
		{BPChar, Name, Implicit, strings.Repeat("a", 62) + " X", strings.Repeat("a", 62)},
		{Name, Text, Implicit, " a  ", " a  "}, {Name, VarChar, Assignment, " a  ", " a  "}, {Name, BPChar, Assignment, " a  ", " a  "},
	} {
		p, err := PlanCoercion(ds[tc.source].Usage(), ds[tc.target].Usage(), tc.mode)
		if err != nil {
			t.Fatal(tc, err)
		}
		v, err := p.Apply(ctx, String(ds[tc.source], tc.in), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Text()
		if err != nil || got != tc.want || v.Type() != ds[tc.target] {
			t.Fatal(tc, got, err)
		}
		v, err = p.Apply(ctx, Null(ds[tc.source]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != ds[tc.target] {
			t.Fatal(v, err)
		}
	}
	for _, value := range []bool{true, false} {
		p, err := PlanCoercion(ds[Bool].Usage(), ds[Name].Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Apply(ctx, Boolean(ds[Bool], value), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := v.Text()
		want := "f"
		if value {
			want = "t"
		}
		if got != want {
			t.Fatal(got, want)
		}
	}
	for _, tc := range []struct {
		source, target ID
		mode           CoercionContext
	}{{Name, Int4, Assignment}, {Bool, Name, Implicit}, {Name, VarChar, Implicit}, {Name, BPChar, Implicit}, {Text, Name, Explicit}, {Name, Text, Explicit}} {
		if _, err := PlanCoercion(ds[tc.source].Usage(), ds[tc.target].Usage(), tc.mode); err == nil {
			t.Fatal("unbounded coercion", tc)
		}
	}
	foreign := testTypes()[Name]
	p, err := PlanCoercion(ds[Name].Usage(), ds[Name].Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Apply(ctx, String(foreign, "x"), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("foreign value accepted")
	}
}

func TestNameComparison(t *testing.T) {
	ds := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		left, right ID
		a, b        string
		want        int
	}{
		{Name, Name, "A", "a", -1}, {Name, Name, "a", "a ", -1}, {Name, Name, "é", "z", 1},
		{Name, Text, strings.Repeat("a", 63), strings.Repeat("a", 64), -1}, {Text, Name, strings.Repeat("a", 64), strings.Repeat("a", 63), 1},
		{Name, Text, "a ", "a", 1}, {Text, Name, "a ", "a", 1},
	} {
		p, err := BindComparison(ds[tc.left], ds[tc.right], ds[tc.left].Usage().Collation, false)
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := p.Compare(ctx, String(ds[tc.left], tc.a), String(ds[tc.right], tc.b), InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, op := range []ComparisonOperator{CompareEqual, CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual} {
		p, err := BindComparisonPredicate(op, ds[Name], ds[Name], ds[Bool], ds[Name].Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, operands := range [][2]Value{{Null(ds[Name]), String(ds[Name], "x")}, {String(ds[Name], "x"), Null(ds[Name])}, {Null(ds[Name]), Null(ds[Name])}} {
			v, err := p.Evaluate(ctx, operands[0], operands[1], InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != ds[Bool] {
				t.Fatal(v, err)
			}
		}
	}
	if _, err := BindComparison(ds[Name], ds[Bool], ds[Name].Usage().Collation, false); err == nil {
		t.Fatal("NAME/BOOL comparison admitted")
	}
}
