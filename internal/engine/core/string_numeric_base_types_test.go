package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStringNumericBaseMD5Rebinding(t *testing.T) {
	e := engine.New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	u := b.Type(sqltype.Text).Usage()
	value := sqltype.String(u.Type, "abc")
	arg := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: value}
	node, err := b.StringNode("md5", u, parser.Position{}, []expr.Expr{arg}, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	replacements := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := rebound.(functions.String)
	got, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || got.Type() != replacements[sqltype.Text] {
		t.Fatal(got, err)
	}
	actual, failure := got.Text()
	if failure != nil || actual != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(actual, failure)
	}
}
