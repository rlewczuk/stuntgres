package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Runtime is the evaluation capability expression nodes need beyond
// expr.Context. The engine's evaluation context is its only implementation.
// Statement-only nodes use BeginEvaluation and the expr.Context environments;
// nodes that read frame state delegate their whole evaluation to an Eval
// method here, so the node types stay free of engine state while evaluation
// logic stays in the engine.
type Runtime interface {
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
	RowAbsent(RowExpr) (bool, error)

	// FrameValue is the plain column read of slot in the frame (scope,
	// frame), checked against usage; ParameterValue reads a statement
	// parameter. Both take scalar coordinates so the hot path copies no node.
	FrameValue(scope, frame uint32, slot int, usage sqltype.Usage) (sqltype.Value, error)
	ParameterValue(number int, usage sqltype.Usage) (sqltype.Value, error)

	// Frame nodes: each reads the row, frame, query or domain state of the
	// current evaluation frame or one of its outer frames.
	EvalColumn(ColumnExpr) (sqltype.Value, error)
	EvalGroup(GroupExpr) (sqltype.Value, error)
	EvalGrouping(GroupingExpr) (sqltype.Value, error)
	EvalTableOID(TableOIDExpr) (sqltype.Value, error)
	EvalReturningColumn(ReturningColumnExpr) (sqltype.Value, error)
	EvalReturningWholeRow(ReturningWholeRowExpr) (sqltype.Value, error)
	EvalMergeAction(MergeActionExpr) (sqltype.Value, error)
	EvalCachedOrder(CachedOrderExpr) (sqltype.Value, error)
	EvalWindow(*WindowExpr) (sqltype.Value, error)
	EvalDomainValue(DomainValueExpr) (sqltype.Value, error)
	EvalUpdateOutput(UpdateOutputExpr) (sqltype.Value, error)

	// Executor nodes: each runs a query or routine body for the current frame.
	EvalSubquery(SubqueryExpr) (sqltype.Value, error)
	EvalSQLRoutine(SQLRoutineExpr) (sqltype.Value, error)

	// Installed extension nodes: each checks its captured identity against the
	// engine's extension providers or catalog.
	EvalInstalledText(InstalledTextExpr) (sqltype.Value, error)
	EvalInstalledCubeSpatial(InstalledCubeSpatialExpr) (sqltype.Value, error)
	OpenExtensionPaths(ExtensionPathsExpr) ([]sqltype.Value, error)
}

// nodeRuntimeOf recovers the node runtime. A nil or foreign context yields the
// prologue's missing-context error.
func nodeRuntimeOf(ctx expr.Context) (Runtime, error) {
	runtime, ok := ctx.(Runtime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing expression context")
	}
	return runtime, nil
}

// BeginEvaluation recovers the node runtime and runs the node prologue.
func BeginEvaluation(ctx expr.Context) (Runtime, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return nil, err
	}
	return runtime, runtime.BeginEvaluation()
}
