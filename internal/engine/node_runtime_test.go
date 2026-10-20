package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// foreignContext satisfies expr.Context without the engine node runtime.
type foreignContext struct{ expr.Context }

func TestNodeRuntimePrologue(t *testing.T) {
	for name, ctx := range map[string]expr.Context{
		"nil":     nil,
		"typed":   (*exec.Context)(nil),
		"foreign": foreignContext{},
	} {
		if _, err := plan.BeginEvaluation(ctx); err == nil || err.Error() != "stuntgres: missing expression context" {
			t.Fatalf("%s context: got %v", name, err)
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}
	if _, err := plan.BeginEvaluation(c); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: got %v", err)
	}

	// The prologue wires domain checks before nodes read their input.
	c = &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	c.DomainValueActive = true
	if _, err := plan.BeginEvaluation(c); err != nil {
		t.Fatal(err)
	}
	if c.FunctionInput().DomainChecks != sqltype.DomainCheckEvaluator(c) {
		t.Fatal("domain checks are not wired to the evaluation context")
	}
}
