package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestExclusionGiSTOperators(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id                     ID
		class, op, left, right string
		inputs                 ID
		match                  bool
	}{
		{Inet, "inet_ops", "=", "10.0.0.1", "10.0.0.1", Inet, true},
		{Inet, "inet_ops", "=", "10.0.0.1", "10.0.0.2", Inet, false},
		{Inet, "inet_ops", "&&", "10.0.0.1/24", "10.0.0.2", Inet, true},
		{Inet, "inet_ops", "&&", "10.0.0.1/24", "10.0.1.2", Inet, false},
		{Int4Range, "range_ops", "&&", "[1,5)", "[3,7)", 3831, true},
		{Int4Range, "range_ops", "&&", "[1,5)", "[5,7)", 3831, false},
		{Int4Range, "range_ops", "&&", "empty", "[3,7)", 3831, false},
		{Int4Range, "range_ops", "&&", "(,)", "[3,7)", 3831, true},
	} {
		t.Run(tc.class+tc.op+tc.left+tc.right, func(t *testing.T) {
			typ := types[tc.id]
			class, ok := LookupGiSTIndexClass(tc.class)
			if !ok || !class.Accepts(typ) {
				t.Fatal(class)
			}
			if _, err := bindTestIndexOrdering(typ.Usage(), class); err == nil {
				t.Fatal("GiST acquired a btree comparator")
			}
			p, err := bindTestExclusionOperator(typ.Usage(), class, tc.op)
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := p.OperatorInputs()
			if err != nil || inputs != ([2]ID{tc.inputs, tc.inputs}) || p.IsEquality() != (tc.op == "=") || !p.MatchesUsage(typ.Usage()) {
				t.Fatal(inputs, p, err)
			}
			left, err := typ.Input(ctx, tc.left, -1)
			if err != nil {
				t.Fatal(err)
			}
			right, err := typ.Input(ctx, tc.right, -1)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := p.Matches(ctx, left, right); err != nil || got != tc.match {
				t.Fatal(got, err)
			}
			if got, err := p.Matches(ctx, Null(typ), right); err != nil || got {
				t.Fatal("strict NULL", got, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := p.Matches(canceled, left, right); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	if _, ok := DefaultGiSTIndexClass(types[Inet]); ok {
		t.Fatal("released INET class is not default")
	}
	if c, ok := DefaultGiSTIndexClass(types[Int4Range]); !ok || c.OID() != 10078 {
		t.Fatal(c, ok)
	}
	if _, err := (ExclusionOperator{}).OperatorInputs(); err == nil {
		t.Fatal("unbound signature")
	}
}
