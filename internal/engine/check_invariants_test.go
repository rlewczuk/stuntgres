package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
	predicate := original.Constraints.Check[0].Expression.(comparisonExpression)
	column := predicate.left.(columnExpression)
	for _, tc := range []struct {
		name   string
		change func(*columnExpression)
	}{
		{"foreign owner", func(c *columnExpression) { c.origin = New().catalog.Domain.Ref(c.origin.Epoch, c.origin.Address) }},
		{"foreign relation", func(c *columnExpression) { c.origin.Address.ObjectID++ }},
		{"foreign epoch", func(c *columnExpression) { c.origin.Epoch++ }},
		{"stale attribute", func(c *columnExpression) { c.origin.Address.SubID++ }},
		{"wrong dense slot", func(c *columnExpression) { c.slot++ }},
		{"wrong scope", func(c *columnExpression) { c.scope++ }},
		{"wrong frame", func(c *columnExpression) { c.frame++ }},
		{"foreign type", func(c *columnExpression) { c.Usage.Type, _ = New().CatalogRead().LookupType(sqltype.Int4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *original
			copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
			bad := column
			tc.change(&bad)
			p := predicate
			p.left = bad
			copy.Constraints.Check[0].Expression = p
			if err := validateChecks(view, &copy, map[string]bool{}); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
	for _, node := range []expr.Expr{
		parameterExpression{Info: expr.Info{Usage: predicate.Usage}, number: 1},
		aggregateExpression{Info: expr.Info{Usage: predicate.Usage}, slot: 0},
	} {
		copy := *original
		copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
		copy.Constraints.Check[0].Expression = node
		if err := validateChecks(view, &copy, map[string]bool{}); err == nil {
			t.Fatalf("unpersistable %T accepted", node)
		}
	}
	copy := *original
	copy.Constraints.Check = append([]catalog.CheckConstraint(nil), original.Constraints.Check...)
	copy.Constraints.Check[0].Dependencies = nil
	if err := validateChecks(view, &copy, map[string]bool{}); err == nil {
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
	eval := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Row: old, Scope: 7, Frame: 9}}
	for _, n := range []int64{1, 2} {
		v, err := sqltype.Integer(table.Columns[0].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		c, err := validateCheckRow(table, []sqltype.Value{v}, &eval)
		if err != nil || (c != nil) != (n == 2) {
			t.Fatal(n, c, err)
		}
	}
	if c, err := validateCheckRow(table, old, &eval); err != nil || c != nil {
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
			if err := validateChecks(target.CatalogRead(), d.(*catalog.Relation), map[string]bool{}); err != nil {
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
