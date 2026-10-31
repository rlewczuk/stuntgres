package tables_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
)

func TestDefaultArithmeticPlanningScopedPolicy(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(i INT DEFAULT 1/0)")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	node := view.MustRelation(ref.Address).ColumnMeta.Defaults[1].Expression
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	ordinary, changed, err := exec.PreflightCoercions(ctx, node)
	if err != nil || changed {
		t.Fatal("ordinary arithmetic must remain row-owned", changed, err)
	}
	if _, ok := ordinary.(plan.ArithmeticExpr); !ok {
		t.Fatal("ordinary arithmetic was folded")
	}
	_, _, err = exec.PreflightCoercionsWithPolicy(ctx, node, true)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
		t.Fatal(err)
	}
	if _, ok := view.MustRelation(ref.Address).ColumnMeta.Defaults[1].Expression.(plan.ArithmeticExpr); !ok {
		t.Fatal("catalog expression changed during execution planning")
	}
}
