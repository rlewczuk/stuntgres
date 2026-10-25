package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgcryptoPasswordVolatility(t *testing.T) {
	e, _ := pgcryptoUnitInstalled(t)
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for _, v := range []struct {
		name       string
		args       []parser.Expr
		volatility expr.Volatility
	}{
		{"crypt", []parser.Expr{literal("foox"), literal("$2a$04$......................")}, expr.Immutable},
		{"gen_salt", []parser.Expr{literal("bf")}, expr.Volatile},
	} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: v.name}}}, Args: v.args}
		node, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledCryptoExpr)
		if n.ResultUsage().Type.ID() != sqltype.Text || n.Properties().Volatility != v.volatility || exec.PlannedVolatility(n) != v.volatility || plan.GenerationImmutable(n) != (v.volatility == expr.Immutable) {
			t.Fatal(v, n)
		}
		if err := stored.ValidateInstalledCrypto(n, e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		if !analyze.ClosedPgcryptoDefault(n) {
			t.Fatal("closed installed crypto default rejected", v.name)
		}
		persisted, err := binder.BindDefault(raw, catalog.Column{Name: "salt", Usage: binder.Type(sqltype.Text).Usage()})
		if err != nil {
			t.Fatal("installed crypto DEFAULT admission", v.name, err)
		}
		if _, err := stored.DefaultSQL(context.Background(), e.CatalogRead(), persisted); err != nil {
			t.Fatal("installed crypto DEFAULT rendering", v.name, err)
		}
		for _, properties := range []expr.Properties{
			{Volatility: expr.Volatile, RowDependent: true},
			{Volatility: expr.Volatile, ParameterDependent: true},
			{Volatility: expr.Volatile, StageDependent: true},
		} {
			bad := n
			bad.Property = properties
			if analyze.ClosedPgcryptoDefault(bad) {
				t.Fatal("dependent crypto DEFAULT admitted", properties)
			}
		}
		foreign := n
		proc := *n.Procedure
		proc.Implementation = new(*extension.StockPgcrypto)
		foreign.Procedure = &proc
		if analyze.ClosedPgcryptoDefault(foreign) {
			t.Fatal("foreign crypto DEFAULT provider admitted")
		}
		rebuilt, err := n.Reconstruct(n.Info, n.Args)
		if err != nil {
			t.Fatal(err)
		}
		n.Pgcrypto = rebuilt
		if exec.PlannedVolatility(n) != v.volatility || plan.GenerationImmutable(n) != (v.volatility == expr.Immutable) {
			t.Fatal("reconstruction changed intrinsic volatility")
		}
		retagged, _, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil {
			t.Fatal(err)
		}
		if exec.PlannedVolatility(retagged) != v.volatility || plan.GenerationImmutable(retagged) != (v.volatility == expr.Immutable) {
			t.Fatal("retag changed intrinsic volatility")
		}
		value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || value.IsNull() || value.Type().ID() != sqltype.Text {
			t.Fatal(value, err)
		}
	}
	random := functions.Random{Info: expr.Info{Usage: binder.Type(sqltype.Float8).Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	if analyze.ClosedPgcryptoDefault(random) {
		t.Fatal("unrelated volatile DEFAULT admitted")
	}
}
