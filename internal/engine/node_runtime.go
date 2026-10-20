package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// nodeRuntime is the evaluation capability engine expression nodes need
// beyond expr.Context. *EvalContext is its only implementation. Statement-only
// nodes use BeginEvaluation and the expr.Context environments; nodes that read
// frame state delegate their whole evaluation to an Eval method here, so the
// node types stay free of engine state while evaluation logic stays in the
// engine.
type nodeRuntime interface {
	expr.Context

	// BeginEvaluation is the shared node prologue: missing-context check,
	// domain-check wiring while a domain value is active, then cancellation.
	BeginEvaluation() error
	// CoercionEnvironments returns the input and output environments of a
	// coercion: the live statement scope supplies catalog-backed resolvers the
	// evaluation context lacks, and domainChecks wires domain evaluation.
	CoercionEnvironments(domainChecks bool) (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	// RowAbsent reports whether a whole-row reference reads an absent source
	// row of its frame.
	RowAbsent(rowExpression) (bool, error)
}

var _ nodeRuntime = (*EvalContext)(nil)

// nodeRuntimeOf recovers the node runtime. A nil or foreign context yields the
// prologue's missing-context error.
func nodeRuntimeOf(ctx expr.Context) (nodeRuntime, error) {
	runtime, ok := ctx.(nodeRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing expression context")
	}
	return runtime, nil
}

// beginEvaluation recovers the node runtime and runs the node prologue.
func beginEvaluation(ctx expr.Context) (nodeRuntime, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return nil, err
	}
	return runtime, runtime.BeginEvaluation()
}

func (c *EvalContext) BeginEvaluation() error { return evaluationContext(c) }

func (c *EvalContext) CoercionEnvironments(domainChecks bool) (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	output := c.Output
	output.Regclass = c.liveScope()
	output.Regtype = c.liveScope()
	output.Regrole = c.liveScope()
	output.Regcollation = c.liveScope()
	output.Regnamespace = c.liveScope()
	output.Regproc = c.liveScope()
	output.Operator = c.liveScope()
	output.RegTS = c.liveScope()
	output.ACLItem = c.liveScope()
	input := c.Input
	if input.Regrole == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regrole = scope.input.Regrole
		}
	}
	if input.Regcollation == nil || input.Regnamespace == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regcollation, input.Regnamespace = scope.input.Regcollation, scope.input.Regnamespace
		}
	}
	if domainChecks {
		input.DomainChecks = c
	}
	return input, output
}
