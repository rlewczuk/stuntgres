package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalGenericDeclarationReparse(t *testing.T) {
	d := testTypes()[Interval]
	for _, mask := range []int32{32767, 4, 2, 8, 1024, 2048, 4096, 6, 1032, 3080, 7176, 3072, 7168, 6144} {
		for _, precision := range []int32{0, 1, 2, 3, 4, 5, 6, 65535} {
			inputMods := []int32{mask}
			if precision != 65535 {
				inputMods = append(inputMods, precision)
			}
			u, warning, err := d.ResolveModifiers(inputMods...)
			if err != nil || warning != nil {
				t.Fatal(err, warning)
			}
			declaration, err := u.Declaration()
			if err != nil {
				t.Fatal(err)
			}
			statements, err := parser.Parse("CREATE TABLE iv(v " + declaration + ")")
			if err != nil {
				t.Fatal(declaration, err)
			}
			raw := statements[0].(*parser.CreateTable).Elements[0].Column.TypeName
			mods := make([]int32, len(raw.Modifiers))
			for i, m := range raw.Modifiers {
				if m.Integer == nil {
					t.Fatal(raw)
				}
				mods[i] = *m.Integer
			}
			recovered := d.Usage()
			if len(mods) > 0 {
				recovered, warning, err = d.ResolveModifiers(mods...)
			}
			want := mask<<16 | precision
			if mask == 32767 && precision == 65535 {
				want = -1
			}
			if err != nil || warning != nil || u.Typmod != want || recovered.Typmod != want {
				t.Fatal(declaration, recovered, u, want, warning, err)
			}
		}
	}
}

func intervalBoundaryError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, want)
	}
}

func TestIntervalISOMagnitudeDiagnostics(t *testing.T) {
	d := testTypes()[Interval]
	for _, s := range []string{"PT1000000000000001S", "P1YT1000000000000001S", "PT-1000000000000001S", "P1000000000000001Y", "P1Y1000000000000001M", "P2-1000000000000001-1", "P2-1-1000000000000001", "PT1:1000000000000001:1", "PT1:1:1000000000000001"} {
		_, err := d.Input(context.Background(), s, -1)
		intervalBoundaryError(t, err, "22015", "interval field value out of range: \""+s+"\"")
	}
}

func TestIntervalYearMonthImplicitUnit(t *testing.T) {
	d := testTypes()[Interval]
	typmods := []int32{-1}
	for _, mask := range []int32{IntervalYear, IntervalYear | IntervalMonth, IntervalDay, IntervalMinute | IntervalSecond} {
		u, _, err := d.ResolveModifiers(mask)
		if err != nil {
			t.Fatal(err)
		}
		typmods = append(typmods, u.Typmod)
	}
	for _, typmod := range typmods {
		_, err := d.Input(context.Background(), "1 1-2", typmod)
		intervalBoundaryError(t, err, "22007", "invalid input syntax for type interval: \"1 1-2\"")
	}
}

func TestIntervalISOAlternativeExponentWidth(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		s       string
		m, days int32
		us      int64
		out     string
	}{
		{"P00021015e0", 34, 15, 0, "2 years 10 mons 15 days"},
		{"P00021015.0e0", 34, 15, 0, "2 years 10 mons 15 days"},
		{"PT001020e0", 0, 0, 620000000, "00:10:20"},
		{"PT001020.0e0", 0, 0, 620000000, "00:10:20"},
	} {
		v, err := d.Input(context.Background(), tc.s, -1)
		if err != nil {
			t.Fatal(tc.s, err)
		}
		m, days, us, err := v.Interval()
		if err != nil || m != tc.m || days != tc.days || us != tc.us {
			t.Fatal(tc.s, m, days, us, err)
		}
		out, err := d.Output(context.Background(), v)
		if err != nil || out != tc.out {
			t.Fatal(tc.s, out, err)
		}
	}
}

func TestIntervalPrecisionOverflowBranches(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Interval]
	u, _, err := d.ResolveModifiers(IntervalSecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	const input = "2562047788:00:54.775807"
	v, err := d.Input(ctx, input, -1)
	if err != nil {
		t.Fatal(err)
	}
	m, days, us, err := v.Interval()
	if err != nil || m != 0 || days != 0 || us != math.MaxInt64 {
		t.Fatal(m, days, us, err)
	}
	_, err = d.Input(ctx, input, u.Typmod)
	intervalBoundaryError(t, err, "22008", "interval out of range")
	plan, err := PlanCoercion(d.Usage(), u, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Apply(ctx, v)
	intervalBoundaryError(t, err, "22008", "interval out of range")
}
