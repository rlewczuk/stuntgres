package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func arrayRelationalUnitUnnest(t *testing.T, input string) plan.UnnestExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.BindUnnest(arrayInspectUnitRaw("unnest", arrayInspectUnitArray(input)))
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.UnnestExpr)
}
