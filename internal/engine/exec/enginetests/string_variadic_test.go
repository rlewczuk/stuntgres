package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func formatUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, descriptor := range sqltype.Builtins() { types[descriptor.ID()] = descriptor }
	return types
}

type formatWorkContext struct { context.Context; checks int }
func (c *formatWorkContext) Err() error {
	c.checks--
	if c.checks <= 0 { return context.Canceled }
	return nil
}

func TestStringVariadicBoundedCancellationAndSizes(t *testing.T) {
	types := formatUnitTypes()
	for _, name := range []string{"concat", "quote_ident", "quote_literal"} {
		text := strings.Repeat("'a", 8192)
		arg := plan.LiteralExpr{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, Value: sqltype.String(types[sqltype.Text], text)}
		node := functions.StringVariadic{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, Name: name, Args: []expr.Expr{arg}}
		group := exec.FoldCaptureCandidate{}
		ctx := &formatWorkContext{Context: context.Background(), checks: 4}
		var err error
		if name == "concat" {
			_, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		} else {
			_, err = exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, FoldCaptures: &group}}, node)
		}
		if !errors.Is(err, context.Canceled) || group.Private || group.Entries != nil {
			t.Fatal("cancellation exposed a scalar or partial capture", name, err)
		}
	}
	// Pure bounded-size diagnostics moved to functions.TestStringVariadicOutputSize;
	// cancellation must still leave this owner's capture group unpublished.
}