package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStringCoreOrdinaryDeparseDoesNotChangeBoundArguments(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE deparse_args(v CHAR(4),s CHAR(4),e TEXT,CHECK(btrim(v,s)=e))")
	ref := roleTableRef(t, e, "deparse_args")
	relation := e.Catalog.State.MustRelation(ref.Address)
	node := relation.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left.(functions.String)
	args := append([]expr.Expr(nil), node.Args...)
	stringPlan := node.Plan
	for _, pretty := range []bool{false, true} {
		renderer := stored.Deparser{View: e.CatalogRead(), Relation: relation, Pretty: pretty}
		got, err := renderer.SQL(context.Background(), node, stored.DeparseParent{})
		want := "btrim((v)::text, (s)::text)"
		if pretty {
			want = "btrim(v::text, s::text)"
		}
		if err != nil || got != want {
			t.Fatal(pretty, got, err)
		}
		// These row-dependent arguments have no evaluation frame. Rendering
		// must only present the provider's conversion, never evaluate or wrap it.
		if node.Plan != stringPlan || !reflect.DeepEqual(node.Args, args) {
			t.Fatal("rendering changed the retained provider or arguments")
		}
		for _, arg := range node.Args {
			if _, ok := arg.(plan.ColumnExpr); !ok || arg.ResultUsage().Type.ID() != sqltype.BPChar {
				t.Fatal("renderer installed a runtime conversion", arg)
			}
		}
	}
}

func TestStringCoreBinaryTrimSyntaxCopies(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE trim_copies(v BYTEA,s BYTEA,e BYTEA,CHECK(TRIM(LEADING s FROM v)=e))")
	ref := roleTableRef(t, e, "trim_copies")
	relation := e.Catalog.State.MustRelation(ref.Address)
	node := relation.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left.(functions.Binary)
	if node.Syntax != parser.SQLFunctionSyntax {
		t.Fatal("binding lost special binary TRIM")
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	ordinary := node
	ordinary.Syntax = parser.OrdinaryFunctionSyntax
	if !binder.SameGroupExpression(node, ordinary) {
		t.Fatal("presentation syntax changed binary semantic identity")
	}
	grouped, err := binder.RewriteGroup(node, node.Args)
	if err != nil || grouped.(functions.Binary).Syntax != parser.SQLFunctionSyntax {
		t.Fatal("aggregate rewrite lost syntax", grouped, err)
	}
	remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, map[int32]int{1: 1, 2: 0})
	if err != nil || remapped.(functions.Binary).Syntax != parser.SQLFunctionSyntax ||
		remapped.(functions.Binary).Args[0].(plan.ColumnExpr).Slot != 1 || node.Args[0].(plan.ColumnExpr).Slot != 0 {
		t.Fatal("remap lost syntax or mutated original children", remapped, err)
	}
	var replacement *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bytea {
			replacement = d
		}
	}
	rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == sqltype.Bytea {
			return replacement
		}
		return d
	})
	if err != nil || !changed || rebound.(functions.Binary).Syntax != parser.SQLFunctionSyntax || rebound.ResultUsage().Type != replacement {
		t.Fatal("retag lost syntax or provider ownership", rebound, changed, err)
	}
	folded, _, err := exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, node)
	if err != nil || folded.(functions.Binary).Syntax != parser.SQLFunctionSyntax {
		t.Fatal("preflight lost syntax", folded, err)
	}
	got, err := (stored.ScalarRenderer{Owner: relation}).SQL(context.Background(), e.CatalogRead(), node)
	if err != nil || got != `TRIM(LEADING "s" FROM "v")` {
		t.Fatal("canonical replay lost special binary TRIM", got, err)
	}
	invalid := node
	invalid.Syntax = parser.FunctionSyntax(255)
	if _, err := (stored.ScalarRenderer{Owner: relation}).SQL(context.Background(), e.CatalogRead(), invalid); err == nil {
		t.Fatal("stored binary node accepted unrelated special syntax")
	}
}
