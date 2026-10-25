package engine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionKeyExpression(t *testing.T, text string) parser.Expr {
	t.Helper()
	raw, err := parser.Parse("CREATE TABLE p(k int) PARTITION BY RANGE ((" + text + "))")
	if err != nil {
		t.Fatal(err)
	}
	return raw[0].(*parser.CreateTable).PartitionBy.Keys[0].Expression
}

func TestPartitionHostBindKeyExpressionReturnsKeyAndUsedSlots(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	var _ partition.Host = h
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	key, uses, err := h.BindKeyExpression(auth, f.parent, partitionKeyExpression(t, "k + 1"), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if key.Definition == nil || key.Execution == nil || !slices.Equal(key.Attributes, f.parent.Attributes) {
		t.Fatalf("derived key %#v, want definition, execution and the parent's attributes", key)
	}
	if key.Execution.ResultUsage().Type.ID() != sqltype.Int4 || !key.Execution.Properties().RowDependent {
		t.Fatalf("execution usage %v row dependent %v, want a row-dependent integer", key.Execution.ResultUsage(), key.Execution.Properties().RowDependent)
	}
	if !slices.Equal(uses, []int{0}) {
		t.Fatalf("used slots %v, want [0]", uses)
	}
}

// The volatility, constant and generated-column checks belong to
// partition.BindKeys: the host binds a volatile key and returns it.
func TestPartitionHostBindKeyExpressionLeavesVolatilityToCaller(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	key, uses, err := h.BindKeyExpression(auth, f.parent, partitionKeyExpression(t, "random()"), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if exec.PlannedVolatility(key.Execution) == expr.Immutable || key.Execution.Properties().RowDependent || len(uses) != 0 {
		t.Fatalf("volatile key: volatility %v row dependent %v uses %v", exec.PlannedVolatility(key.Execution), key.Execution.Properties().RowDependent, uses)
	}
}
