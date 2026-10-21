package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCheckDescriptorInvariants(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	ref := roleTableRef(t, e, "t")
	view := e.CatalogRead()
	descriptor, _ := view.Descriptor(ref.Address)
	original := descriptor.(*catalog.Relation)
	predicate := original.Constraints.Check[0].Expression.(plan.ComparisonExpr)
	column := predicate.Left.(plan.ColumnExpr)
	for _, tc := range []struct {
		name   string
		change func(*plan.ColumnExpr)
	}{
		{"foreign owner", func(c *plan.ColumnExpr) { c.Origin = New().catalog.Domain.Ref(c.Origin.Epoch, c.Origin.Address) }},
		{"foreign relation", func(c *plan.ColumnExpr) { c.Origin.Address.ObjectID++ }},
		{"foreign epoch", func(c *plan.ColumnExpr) { c.Origin.Epoch++ }},
		{"stale attribute", func(c *plan.ColumnExpr) { c.Origin.Address.SubID++ }},
		{"wrong dense slot", func(c *plan.ColumnExpr) { c.Slot++ }},
		{"wrong scope", func(c *plan.ColumnExpr) { c.Scope++ }},
		{"wrong frame", func(c *plan.ColumnExpr) { c.Frame++ }},
		{"foreign type", func(c *plan.ColumnExpr) { c.Usage.Type, _ = New().CatalogRead().LookupType(sqltype.Int4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *original
			copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
			bad := column
			tc.change(&bad)
			p := predicate
			p.Left = bad
			copy.Constraints.Check[0].Expression = p
			if err := ValidateChecks(view, &copy, map[string]bool{}); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
	for _, node := range []expr.Expr{
		plan.ParameterExpr{Info: expr.Info{Usage: predicate.Usage}, Number: 1},
		plan.AggregateExpr{Info: expr.Info{Usage: predicate.Usage}, Slot: 0},
	} {
		copy := *original
		copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
		copy.Constraints.Check[0].Expression = node
		if err := ValidateChecks(view, &copy, map[string]bool{}); err == nil {
			t.Fatalf("unpersistable %T accepted", node)
		}
	}
	copy := *original
	copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
	copy.Constraints.Check[0].Dependencies = nil
	if err := ValidateChecks(view, &copy, map[string]bool{}); err == nil {
		t.Fatal("missing dependency manifest accepted")
	}
}

func TestCheckCompletedRowValidator(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	ref := roleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	table := d.(*catalog.Relation)
	old := []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Row: old, Scope: 7, Frame: 9}}
	for _, n := range []int64{1, 2} {
		v, err := sqltype.Integer(table.Columns[0].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		c, err := ValidateCheckRow(table, []sqltype.Value{v}, &eval)
		if err != nil || (c != nil) != (n == 2) {
			t.Fatal(n, c, err)
		}
	}
	if c, err := ValidateCheckRow(table, old, &eval); err != nil || c != nil {
		t.Fatal("UNKNOWN rejected", c, err)
	}
	if eval.Scope != 7 || eval.Frame != 9 || &eval.Row[0] != &old[0] {
		t.Fatal("validator leaked row frame")
	}
}

func TestCheckNativeCatalogReplacementOrigins(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "self"}[self], func(t *testing.T) {
			source := New()
			op, done := roleOperation(t, source, "postgres")
			roleExecute(t, source, op, "CREATE TABLE t(a INT CHECK(a=1))")
			roleExecute(t, source, op, "INSERT INTO t VALUES(1)")
			done()
			target := source
			if !self {
				target = New()
			}
			if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			actor, finish := roleOperation(t, target, "postgres")
			defer finish()
			ref := roleTableRef(t, target, "t")
			d, _ := target.CatalogRead().Descriptor(ref.Address)
			if err := ValidateChecks(target.CatalogRead(), d.(*catalog.Relation), map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			ast := statement(t, "INSERT INTO t VALUES(2)")
			result, err := target.Execute(actor, ast, 0, nil)
			if result.Rows != nil || result.Count != 0 {
				t.Fatal(result)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "t_a_check"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "t_a_check"})
		})
	}
}
