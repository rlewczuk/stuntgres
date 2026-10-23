package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayAggregateBinding(t *testing.T) {
	for _, q := range []string{
		`SELECT array_agg('xy'::varchar(4) COLLATE "C") FROM t`,
		`SELECT array_agg('xy'::varchar(4) COLLATE "POSIX") FROM t`,
		"SELECT array_agg(1.2300::numeric(10,4)) FROM t",
		"SELECT array_agg(NULL::integer[]) FROM t",
	} {
		e := New()
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := statement(t, q).(*parser.Select).Targets[0]
		result, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(q, err)
		}
		call := b.Grouping.Aggregates[0]
		argument := call.Arguments[0]
		usage := result.ResultUsage()
		if usage.Typmod != -1 || usage.Collation != argument.ResultUsage().Collation || !result.Properties().StageDependent || !call.IncludeNulls {
			t.Fatal("aggregate usage/admission", usage, argument.ResultUsage())
		}
		if result.ExplicitCollation() != argument.ExplicitCollation() || result.CollationIndeterminate() != argument.CollationIndeterminate() {
			t.Fatal("collation provenance lost")
		}
		if argument.ResultUsage().Type.IsArray() {
			if usage.Type != argument.ResultUsage().Type || call.Operation != functions.AggregateArrayArray {
				t.Fatal("array overload identity")
			}
		} else {
			associated, ok := e.CatalogRead().LookupArray(argument.ResultUsage().Type)
			if !ok || usage.Type != associated || call.Operation != functions.AggregateArrayScalar {
				t.Fatal("coherent associated array identity")
			}
		}
		found := false
		for _, dependency := range b.Dependencies {
			if dependency.Ref.Address.ClassID == ids.TypeClassID && dependency.Ref.Address.ObjectID == ids.OID(usage.Type.ID()) {
				found = true
			}
		}
		if !found {
			t.Fatal("missing result-array dependency")
		}
		again, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || again.(plan.AggregateExpr).Slot != result.(plan.AggregateExpr).Slot {
			t.Fatal("identical aggregate failed state-slot sharing", err)
		}
		other := call
		other.IncludeNulls = false
		if b.SameAggregateCall(call, other) {
			t.Fatal("NULL admission omitted from aggregate identity")
		}
	}
}

func TestArrayAggregateDomainBinding(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.aa_unit_di AS INT",
		"CREATE DOMAIN public.aa_unit_dj AS public.aa_unit_di",
		"CREATE DOMAIN public.aa_unit_da AS INT[]",
		"CREATE DOMAIN public.aa_unit_dc AS public.aa_unit_da",
	} {
		// Existing helpers stage/publish private catalog objects, never execute SQL.
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	di := domainUnitType(t, view, "aa_unit_di").SQLType
	dj := domainUnitType(t, view, "aa_unit_dj").SQLType
	da := domainUnitType(t, view, "aa_unit_da").SQLType
	dc := domainUnitType(t, view, "aa_unit_dc").SQLType
	diArray, ok := view.LookupArray(di)
	if !ok {
		t.Fatal("scalar-domain associated array")
	}
	djArray, ok := view.LookupArray(dj)
	if !ok {
		t.Fatal("chained-domain associated array")
	}
	integer, _ := view.LookupType(sqltype.Int4)
	integerArray, ok := view.LookupArray(integer)
	if !ok {
		t.Fatal("builtin associated array")
	}
	for _, tc := range []struct {
		input, want *sqltype.Descriptor
		arrays      bool
	}{
		{di, diArray, false}, {dj, djArray, false},
		{da, integerArray, true}, {dc, integerArray, true},
		{diArray, diArray, true},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		b.Params.Parameters[1] = tc.input.Usage()
		raw := parser.AggregateExpr{Name: "array_agg", Args: []parser.Expr{parser.ParameterExpr{Number: 1}}}
		result, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		call := b.Grouping.Aggregates[0]
		provider := call.Provider.(*functions.ArrayAggregateProvider)
		if result.ResultUsage().Type != tc.want || provider.ResultUsage().Type != tc.want || provider.Arrays() != tc.arrays || provider.ArgumentType() != call.Arguments[0].ResultUsage().Type {
			t.Fatal("domain overload lost exact catalog generation", tc, provider)
		}
		if !tc.arrays && provider.ResultUsage().Type.Element() != tc.input {
			t.Fatal("scalar domain was flattened")
		}
		found := false
		for _, d := range b.Dependencies {
			if d.Ref.Address.ClassID == ids.TypeClassID && d.Ref.Address.ObjectID == ids.OID(tc.want.ID()) {
				found = true
			}
		}
		if !found {
			t.Fatal("missing concrete domain result dependency")
		}
	}
}
