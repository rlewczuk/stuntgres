package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestComparisonResolutionSignatures(t *testing.T) {
	types := testTypes()
	// REL_18_6 pg_operator.dat and pg_cast.dat: these are selected formal
	// signatures, not merely physical pairs accepted by a datum comparator.
	for _, tc := range []struct{ left, right, formalLeft, formalRight ID }{
		{Name, VarChar, Name, Text}, {VarChar, Name, Text, Name},
		{Name, BPChar, Name, Text}, {BPChar, Name, Text, Name},
		{VarChar, BPChar, BPChar, BPChar}, {Char, Text, Text, Text},
		{PgNodeTree, PgNodeTree, Text, Text},
		{Regtype, Regclass, Oid, Oid}, {Int4, Regrole, Oid, Oid},
		{Time, Interval, Interval, Interval}, {Interval, Time, Interval, Interval},
		{Cidr, Inet, Inet, Inet}, {Bit, VarBit, VarBit, VarBit},
		{Int8, Int4, Int8, Int4}, {Int8, Numeric, Numeric, Numeric},
		{Int8, Float4, Float8, Float4}, {Numeric, Float4, Float8, Float4},
		{Date, Timestamptz, Date, Timestamptz},
	} {
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			got, err := ResolveComparison(op, types[tc.left], types[tc.right])
			want := EqualitySignature{tc.formalLeft, tc.formalRight}
			if err != nil || got != want {
				t.Fatalf("%d %d %d: got %+v, %v; want %+v", tc.left, op, tc.right, got, err, want)
			}
		}
	}
	for _, id := range []ID{ACLItem, Cid, Xid} {
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			_, err := ResolveComparison(op, types[id], types[id])
			allowed := op == CompareEqual || id == Xid && op == CompareNotEqual
			if allowed != (err == nil) {
				t.Fatal("operator-specific candidate availability", id, op, err)
			}
		}
	}
}

func TestComparisonImplicitEligibility(t *testing.T) {
	types := testTypes()
	// REL_18_6 pg_cast.dat: both directions are implicit binary casts.
	for _, pair := range [][2]ID{{Bit, VarBit}, {VarBit, Bit}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Implicit); err != nil {
			t.Fatal("released implicit bit conversion rejected", pair, err)
		}
	}
	for _, pair := range [][2]ID{{Text, Oid}, {Name, VarChar}, {Interval, Time}, {Int4, Xid}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Implicit); err == nil {
			t.Fatal("assignment, explicit or transitive conversion admitted", pair)
		}
	}
	for _, pair := range [][2]ID{{Text, Oid}, {Int4, Xid}, {JSON, JSON}, {JSONPath, JSONPath}, {Void, Void}} {
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			_, err := ResolveComparison(op, types[pair[0]], types[pair[1]])
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
				t.Fatal("forbidden candidate", pair, op, err)
			}
		}
	}
	for _, tc := range []struct {
		left, right *Descriptor
		want        EqualitySignature
	}{
		{nil, nil, EqualitySignature{Text, Text}},
		{types[Name], nil, EqualitySignature{Name, Name}},
		{nil, types[VarChar], EqualitySignature{Text, Text}},
		{types[Regclass], nil, EqualitySignature{Oid, Oid}},
	} {
		got, err := ResolveComparison(CompareEqual, tc.left, tc.right)
		if err != nil || got != tc.want {
			t.Fatal("unknown exact/preferred selection", got, tc.want, err)
		}
	}
}

func TestComparisonProviderInvariants(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{JSON, JSONPath, Void} {
		if equal, order := ComparisonCapabilities(types[id]); equal || order {
			t.Fatal("non-provider acquired default comparison", id)
		}
		if _, err := BindComparison(types[id], types[id], CollationRef{}, false); err == nil {
			t.Fatal("same ID acquired a default-zero comparator", id)
		}
	}
	for _, id := range []ID{ACLItem, Cid, Xid} {
		if equal, order := ComparisonCapabilities(types[id]); !equal || order {
			t.Fatal("equality-only capability", id, equal, order)
		}
	}
}

func TestComparisonDeferredRecordProvider(t *testing.T) {
	types := testTypes()
	deferred := DeferredRecord()
	named := compositeForTest(t, 61000, CompositeMember{1, "x", types[Int4].Usage()})
	for _, pair := range [][2]*Descriptor{{deferred, deferred}, {deferred, named}, {named, deferred}} {
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			signature, err := ResolveComparison(op, pair[0], pair[1])
			if err != nil || signature != (EqualitySignature{pair[0].ID(), pair[1].ID()}) {
				t.Fatal("ordinary RECORD formal admission", signature, err)
			}
			predicate, err := BindComparisonPredicate(op, pair[0], pair[1], types[Bool], CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := predicate.Evaluate(context.Background(), Null(pair[0]), Null(pair[1]), InputEnvironment{})
			if err != nil || !result.IsNull() || result.Type() != types[Bool] {
				t.Fatal("strict RECORD NULL demanded nonexistent layout", result, err)
			}
		}
	}
}
