package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgcryptoPasswordVolatility(t *testing.T) {
	e, _ := pgcryptoUnitInstalled(t)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for _, v := range []struct {
		name       string
		args       []parser.Expr
		volatility Volatility
	}{
		{"crypt", []parser.Expr{literal("foox"), literal("$2a$04$......................")}, Immutable},
		{"gen_salt", []parser.Expr{literal("bf")}, Volatile},
	} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: v.name}}}, Args: v.args}
		node, err := binder.bindInstalledCrypto(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(installedCryptoFunctionExpression)
		if n.ResultUsage().Type.ID() != sqltype.Text || n.Properties().Volatility != v.volatility || plannedVolatility(n) != v.volatility || generationImmutable(n) != (v.volatility == Immutable) {
			t.Fatal(v, n)
		}
		if err := n.validate(e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		if !closedPgcryptoDefault(n) {
			t.Fatal("closed installed crypto default rejected", v.name)
		}
		stored, err := binder.bindDefault(raw, ColumnDescriptor{Name: "salt", Usage: binder.typ(sqltype.Text).Usage()})
		if err != nil {
			t.Fatal("installed crypto DEFAULT admission", v.name, err)
		}
		if _, err := defaultSQL(context.Background(), e.CatalogRead(), stored); err != nil {
			t.Fatal("installed crypto DEFAULT rendering", v.name, err)
		}
		for _, properties := range []ExprProperties{
			{Volatility: Volatile, RowDependent: true},
			{Volatility: Volatile, ParameterDependent: true},
			{Volatility: Volatile, StageDependent: true},
		} {
			bad := n
			bad.Property = properties
			if closedPgcryptoDefault(bad) {
				t.Fatal("dependent crypto DEFAULT admitted", properties)
			}
		}
		foreign := n
		proc := *n.procedure
		proc.crypto = &pgcryptoProvider{version: "1.4"}
		foreign.procedure = &proc
		if closedPgcryptoDefault(foreign) {
			t.Fatal("foreign crypto DEFAULT provider admitted")
		}
		rebuilt, err := n.Reconstruct(n.Info, n.Args)
		if err != nil {
			t.Fatal(err)
		}
		n.Pgcrypto = rebuilt
		if plannedVolatility(n) != v.volatility || generationImmutable(n) != (v.volatility == Immutable) {
			t.Fatal("reconstruction changed intrinsic volatility")
		}
		retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil {
			t.Fatal(err)
		}
		if plannedVolatility(retagged) != v.volatility || generationImmutable(retagged) != (v.volatility == Immutable) {
			t.Fatal("retag changed intrinsic volatility")
		}
		value, err := n.Eval(&EvalContext{Context: context.Background()})
		if err != nil || value.IsNull() || value.Type().ID() != sqltype.Text {
			t.Fatal(value, err)
		}
	}
	random := functions.Random[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: binder.typ(sqltype.Float8).Usage(), properties: ExprProperties{Volatility: Volatile}}), Name: "random"}
	if closedPgcryptoDefault(random) {
		t.Fatal("unrelated volatile DEFAULT admitted")
	}
}
