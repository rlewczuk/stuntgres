package partition_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparsePartitionBoundCapture(t *testing.T) {
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ForeignRelation} {
		f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
		f.child.RelationKind = kind
		f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "0")}
		f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "10")}
		source := f.view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ClassOID})
		row, err := pgcatalog.ClassRow(pgcatalog.Input{Context: t.Context(), Catalog: f.view, Source: source}, f.child)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := row[33].Tree()
		if err != nil {
			t.Fatal(err)
		}
		bound, keys, ok := pgcatalog.PartitionBoundParts(tree)
		if !ok || bound != f.child.Membership || &keys[0] != &f.parent.Partition.Keys[0] {
			t.Fatal("catalog carrier lost captured descriptor backing")
		}
		text, _ := f.view.LookupType(sqltype.Text)
		oid, _ := f.view.LookupType(sqltype.Oid)
		id, err := sqltype.OIDValue(oid, uint32(f.child.Address().ObjectID))
		if err != nil {
			t.Fatal(err)
		}
		values := [3]sqltype.Value{row[33], id}
		check := func() {
			t.Helper()
			for _, signature := range []functions.DeparseSignature{functions.DeparseExpr, functions.DeparseExprPretty} {
				boolean, _ := f.view.LookupType(sqltype.Bool)
				for _, pretty := range []bool{false, true} {
					values[2] = sqltype.Boolean(boolean, pretty)
					got, err := partition.Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}, f.input, signature, values, text)
					if err != nil {
						t.Fatal(err)
					}
					if output, err := got.Text(); err != nil || output != "FOR VALUES FROM (0) TO (10)" {
						t.Fatal("captured bound presentation", output, err)
					}
				}
			}
		}
		check()
		replacement := *f.child
		replacement.Membership = &catalog.PartitionMembership{Parent: f.parent.Address(), DefaultBound: true}
		f.put(&replacement)
		f.view.State().RemoveDescriptor(f.parent.Address())
		check()
		if bound != f.child.Membership || &keys[0] != &f.parent.Partition.Keys[0] {
			t.Fatal("deparse changed captured backing")
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := partition.Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}, f.input, functions.DeparseExpr, values, text); !errors.Is(err, context.Canceled) {
			t.Fatal("bound deparse lost cancellation", err)
		}
		f.view.State().RemoveDescriptor(f.child.Address())
		got, err := partition.Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}, f.input, functions.DeparseExpr, values, text)
		if err != nil || !got.IsNull() || got.Type() != text {
			t.Fatal("missing expression relation did not return typed NULL", got, err)
		}
	}
}

func TestDeparsePartitionExpressionDelegation(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
	integer, _ := f.view.LookupType(sqltype.Int4)
	nodeTree, _ := f.view.LookupType(sqltype.PgNodeTree)
	text, _ := f.view.LookupType(sqltype.Text)
	oid, _ := f.view.LookupType(sqltype.Oid)
	id, err := sqltype.OIDValue(oid, 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	tree := plan.BoundTree{Expression: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}}
	if _, _, ok := pgcatalog.PartitionBoundParts(tree); ok {
		t.Fatal("ordinary expression acquired partition-bound authority")
	}
	carrier, err := sqltype.Tree(nodeTree, tree)
	if err != nil {
		t.Fatal(err)
	}
	got, err := partition.Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}, f.input, functions.DeparseExpr, [3]sqltype.Value{carrier, id}, text)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := got.Text(); err != nil || output != "7" {
		t.Fatal("ordinary expression did not reach shared table deparse", output, err)
	}
}
