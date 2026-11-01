package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func isnUnitTypes(t *testing.T) []*sqltype.Descriptor {
	t.Helper()
	out := make([]*sqltype.Descriptor, 8)
	for i, makeType := range []func(sqltype.ID, string, string) (*sqltype.Descriptor, error){sqltype.Ean13, sqltype.Isbn, sqltype.Isbn13, sqltype.Ismn, sqltype.Ismn13, sqltype.Issn, sqltype.Issn13, sqltype.Upc} {
		var err error
		out[i], err = makeType(sqltype.ID(60000+i), "renamed", "other.renamed")
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

var isnUnitNumbers = []string{"9781234567897", "9781234567897", "9781234567897", "9790123456785", "9790123456785", "9771234567898", "9771234567898", "0123456789012"}

func isnUnitInput(t *testing.T, d *sqltype.Descriptor, s string) sqltype.Value {
	t.Helper()
	v, err := d.Input(context.Background(), s, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestIsnComparison(t *testing.T) {
	types := isnUnitTypes(t)
	var boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bool {
			boolean = d
		}
	}
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	admitted := 0
	for a, left := range types {
		for b, right := range types {
			want := a == b || a == 0 || b == 0 || a > 0 && a < 7 && b > 0 && b < 7 && (a-1)/2 == (b-1)/2
			comparison, err := sqltype.BindComparison(left, right, sqltype.CollationRef{}, false)
			if (err == nil) != want {
				t.Fatalf("pair %d,%d: %v", a, b, err)
			}
			if !want {
				continue
			}
			admitted++
			role := a
			if role == 0 {
				role = b
			}
			plain := isnUnitInput(t, left, isnUnitNumbers[role])
			marked := isnUnitInput(t, right, isnUnitNumbers[role]+"!")
			if sign, err := comparison.Compare(ctx, plain, marked, sqltype.InputEnvironment{}); err != nil || sign != -1 {
				t.Fatal("packed flag order", a, b, sign, err)
			}
			if _, err := comparison.Compare(canceled, plain, marked, sqltype.InputEnvironment{}); err != context.Canceled {
				t.Fatal(err)
			}
			foreign := isnUnitTypes(t)[a]
			if _, err := comparison.Compare(ctx, isnUnitInput(t, foreign, isnUnitNumbers[role]), marked, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("equal-ID foreign generation")
			}
			for op := sqltype.CompareEqual; op <= sqltype.CompareGreaterEqual; op++ {
				p, err := sqltype.BindComparisonPredicate(op, left, right, boolean, sqltype.CollationRef{}, false)
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.Evaluate(ctx, plain, marked, sqltype.InputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				truth, err := got.Boolean()
				if err != nil || truth != []bool{false, true, true, true, false, false}[op] {
					t.Fatal(a, b, op, truth, err)
				}
				for _, values := range [][2]sqltype.Value{{sqltype.Null(left), marked}, {plain, sqltype.Null(right)}, {sqltype.Null(left), sqltype.Null(right)}} {
					v, err := p.Evaluate(ctx, values[0], values[1], sqltype.InputEnvironment{})
					if err != nil || !v.IsNull() || v.Type() != boolean {
						t.Fatal("strict NULL", v, err)
					}
				}
			}
		}
	}
	if admitted != 28 {
		t.Fatal(admitted)
	}
	for _, role := range []int{5, 6} {
		d := types[role]
		p, err := sqltype.BindComparison(d, d, sqltype.CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		sign, err := p.Compare(ctx, isnUnitInput(t, d, "9771234567003"), isnUnitInput(t, d, "9771234567898"), sqltype.InputEnvironment{})
		if err != nil || sign != -1 {
			t.Fatal("ISSN issue digits lost", sign, err)
		}
	}
}

func TestIsnCoercion(t *testing.T) {
	types := isnUnitTypes(t)
	ctx := context.Background()
	count := 0
	for a, from := range types {
		for b, to := range types {
			if a == b {
				continue
			}
			edge := a == 0 || b == 0 || a > 0 && a < 7 && b > 0 && b < 7 && (a-1)/2 == (b-1)/2
			if edge {
				count++
			}
			for _, mode := range []sqltype.CoercionContext{sqltype.Implicit, sqltype.Assignment, sqltype.Explicit} {
				want := edge && (mode == sqltype.Explicit || mode == sqltype.Assignment && a != 0)
				p, err := sqltype.PlanCoercion(from.Usage(), to.Usage(), mode)
				if (err == nil) != want {
					t.Fatal(a, b, mode, err)
				}
				if !want {
					continue
				}
				functions := 0
				if a == 0 {
					functions = 1
				}
				if p.FunctionExpression() != (a == 0) || p.EvaluationFunctionCount() != functions {
					t.Fatal("directional expression shape", a, b)
				}
				role := a
				if a == 0 {
					role = b
				}
				for _, suffix := range []string{"", "!"} {
					v := isnUnitInput(t, from, isnUnitNumbers[role]+suffix)
					got, err := p.Apply(ctx, v, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
					if err != nil || got.Type() != to {
						t.Fatal(err)
					}
					same, err := v.SamePayload(ctx, got)
					if err != nil || !same {
						t.Fatal("packed payload not preserved", a, b, err)
					}
				}
				v, err := p.Apply(ctx, sqltype.Null(from), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
				if err != nil || !v.IsNull() || v.Type() != to {
					t.Fatal("typed NULL", err)
				}
				foreign := isnUnitTypes(t)[a]
				if _, err := p.Apply(ctx, sqltype.Null(foreign), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
					t.Fatal("foreign source pointer")
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := p.Apply(canceled, sqltype.Null(from), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != context.Canceled {
					t.Fatal(err)
				}
			}
		}
	}
	if count != 20 {
		t.Fatal(count)
	}
	for target := 1; target < 8; target++ {
		p, err := sqltype.PlanCoercion(types[0].Usage(), types[target].Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Apply(ctx, isnUnitInput(t, types[0], "1234567890128"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
			t.Fatal("generic EAN narrowed", target)
		}
	}
}
