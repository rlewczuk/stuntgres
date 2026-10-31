package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDefaultDescriptorProviderClosedDefinitions(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE d(i INT DEFAULT 2147483648,b BOOL DEFAULT (1 < 2))`)
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, old); err != nil {
		t.Fatal("validation evaluated narrowing", err)
	}
	names := []string{"missing-attribute", "wrong-destination", "missing-dependencies", "parameter-dependent-valid-oid", "nonnull-literal-type-mismatch-valid-oid", "missing-oid"}
	for i, mutate := range []func(*catalog.Relation){
		func(d *catalog.Relation) {
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{99: old.ColumnMeta.Defaults[1]}
		},
		func(d *catalog.Relation) {
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: old.ColumnMeta.Defaults[2]}
		},
		func(d *catalog.Relation) {
			bad := old.ColumnMeta.Defaults[1]
			bad.Dependencies = nil
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
		},
		func(d *catalog.Relation) {
			u := old.Columns[0].Usage
			node := plan.ParameterExpr{Info: expr.Info{Usage: u, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
			bad := old.ColumnMeta.Defaults[1]
			bad.Expression = node
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, node); err == nil || err.Error() != "stuntgres: invalid stored default properties" {
				t.Fatal("parameter invariant not reached", err)
			}
		},
		func(d *catalog.Relation) {
			u := old.Columns[0].Usage
			node := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: sqltype.Boolean(old.Columns[1].Usage.Type, true)}
			bad := old.ColumnMeta.Defaults[1]
			bad.Expression = node
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, node); err == nil || err.Error() != "stuntgres: invalid stored literal" {
				t.Fatal("literal type invariant not reached", err)
			}
		},
		func(d *catalog.Relation) {
			bad := old.ColumnMeta.Defaults[1]
			bad.OID = 0
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if err := (core.RelationProvider).Validate(view, d); err == nil || err.Error() != "stuntgres: invalid column default destination" {
				t.Fatal("missing identity invariant not reached", err)
			}
		},
	} {
		t.Run(names[i], func(t *testing.T) {
			next := *old
			mutate(&next)
			want := "stuntgres: invalid column default expression"
			if i < 2 || i == 5 {
				want = "stuntgres: invalid column default destination"
			}
			if err := (core.RelationProvider).Validate(view, &next); err == nil || err.Error() != want {
				t.Fatal("wrong invariant failure", next.ColumnMeta.Defaults, err, want)
			}
		})
	}
}

func TestDefaultDescriptorProviderClosedCasts(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE d(t TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT CAST('oops'::text AS INT))`)
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, old); err != nil {
		t.Fatal("definition validation evaluated an explicit cast", err)
	}
	text, err := stored.DefaultSQL(context.Background(), view, old.ColumnMeta.Defaults[2].Expression)
	if err != nil || !strings.Contains(text, "CAST(CAST('oops' AS TEXT) AS ") {
		t.Fatal("lost known TEXT input type", text, err)
	}
	for _, mutate := range []func(plan.CoercionExpr) plan.CoercionExpr{
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.CoercionContext(255); return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.Implicit; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.Assignment; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Plan = sqltype.CoercionPlan{}; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Child = nil; return n },
	} {
		next := *old
		bad := old.ColumnMeta.Defaults[1]
		bad.Expression = mutate(bad.Expression.(plan.CoercionExpr))
		next.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad, 2: old.ColumnMeta.Defaults[2]}
		if err := (core.RelationProvider).Validate(view, &next); err == nil {
			t.Fatal("invalid cast definition accepted", bad)
		}
	}
}
