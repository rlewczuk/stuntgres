package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestPGDependRegprocValueReferences(t *testing.T) {
	f := newCCRUUnitFixture(t)
	view := f.f.e.Catalog.Read(f.state)
	for _, id := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure} {
		typ, ok := view.LookupType(id)
		if !ok {
			t.Fatal("missing routine alias", id)
		}
		t.Run(typ.Name(), func(t *testing.T) {
			value, err := sqltype.RegprocValue(typ, uint32(f.routine.Address().ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			zero, err := sqltype.RegprocValue(typ, 0)
			if err != nil {
				t.Fatal(err)
			}
			missing, err := sqltype.RegprocValue(typ, 999999)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name  string
				value sqltype.Value
				want  []ids.ObjectAddress
			}{
				{"routine", value, []ids.ObjectAddress{f.routine.Address()}},
				{"null", sqltype.Null(typ), nil},
				{"zero", zero, nil},
				{"missing", missing, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: tc.value}
					refs, err := stored.ExpressionDependRefs(t.Context(), view, literal)
					if err != nil {
						t.Fatal(err)
					}
					pgDependUnitRefs(t, refs, tc.want)
					refs, err = stored.QueryDependRefs(t.Context(), view, nil, &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{literal, literal}}})
					if err != nil {
						t.Fatal(err)
					}
					pgDependUnitRefs(t, refs, tc.want)
				})
			}
			// Both table and domain recipes consume the same literal semantic
			// authority as pg_depend; no view-specific alias handling is involved.
			state := catalog.CloneState(f.state)
			boolean, _ := view.LookupType(sqltype.Bool)
			check := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}, Not: true}
			other := *state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24102})
			other.Constraints.Check = nil
			state.SetDescriptor(other.Address(), &other)
			table := *state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24101})
			table.Constraints.Check = []catalog.CheckConstraint{{OID: 24120, Name: "same", Expression: check}}
			state.SetDescriptor(table.Address(), &table)
			domain := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 24110})
			domain.DomainChecks = []catalog.DomainCheckConstraint{{OID: 24122, Name: "domain_check", Expression: check}}
			state.SetDescriptor(domain.Address(), &domain)
			ccruUnitRows(t, f.input(t, state, f.f.owner), ccruUnitProduce(t), ccruUnitExpected("routine_24100"))
		})
	}
	text, _ := view.LookupType(sqltype.Text)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "routine()")}
	refs, err := stored.ExpressionDependRefs(t.Context(), view, literal)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, nil)
}
