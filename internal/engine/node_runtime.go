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
	// ApplyCoercion applies a coercion plan with the coercion environments:
	// the live statement scope supplies catalog-backed resolvers the
	// evaluation context lacks, and domainChecks wires domain evaluation.
	ApplyCoercion(plan sqltype.CoercionPlan, value sqltype.Value, domainChecks bool) (sqltype.Value, error)
	// RowAbsent reports whether a whole-row reference reads an absent source
	// row of its frame.
	RowAbsent(rowExpression) (bool, error)

	// FrameValue is the plain column read of slot in the frame (scope,
	// frame), checked against usage; ParameterValue reads a statement
	// parameter. Both take scalar coordinates so the hot path copies no node.
	FrameValue(scope, frame uint32, slot int, usage sqltype.Usage) (sqltype.Value, error)
	ParameterValue(number int, usage sqltype.Usage) (sqltype.Value, error)

	// Frame nodes: each reads the row, frame, query or domain state of the
	// current evaluation frame or one of its outer frames.
	EvalColumn(columnExpression) (sqltype.Value, error)
	EvalGroup(groupExpression) (sqltype.Value, error)
	EvalGrouping(groupingExpression) (sqltype.Value, error)
	EvalTableOID(tableOIDExpression) (sqltype.Value, error)
	EvalReturningColumn(returningColumnExpression) (sqltype.Value, error)
	EvalReturningWholeRow(returningWholeRowExpression) (sqltype.Value, error)
	EvalMergeAction(mergeActionExpression) (sqltype.Value, error)
	EvalCachedOrder(cachedOrderExpression) (sqltype.Value, error)
	EvalWindow(*windowExpression) (sqltype.Value, error)
	EvalDomainValue(domainValueExpression) (sqltype.Value, error)
	EvalUpdateOutput(updateOutputExpression) (sqltype.Value, error)

	// Executor nodes: each runs a query or routine body for the current frame.
	EvalSubquery(subqueryExpression) (sqltype.Value, error)
	EvalSQLRoutine(sqlRoutineExpression) (sqltype.Value, error)
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

func (c *EvalContext) ApplyCoercion(plan sqltype.CoercionPlan, value sqltype.Value, domainChecks bool) (sqltype.Value, error) {
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
	return plan.Apply(c.Context, value, input, output)
}
