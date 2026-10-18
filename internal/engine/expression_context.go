package engine

import (
	"context"
	"time"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// ExprContext is the evaluation runtime visible to expression nodes outside
// the engine's own node set. It becomes expr.Context when the expression
// contract leaves the engine. *EvalContext is the only implementation; engine
// nodes recover it with evalContext and keep engine-private state there.
//
// Every method is a per-evaluation capability of the current statement. None
// may be backed by package-level state.
type ExprContext interface {
	// FunctionContext returns the statement's cancellation context, or nil
	// when no evaluation context is available.
	FunctionContext() context.Context

	// Presentation environments. Input and output follow the session; the
	// JSON, string and format outputs add the live statement scope for
	// catalog-backed types such as regclass and aclitem.
	FunctionInput() sqltype.InputEnvironment
	FunctionOutput() sqltype.OutputEnvironment
	FunctionJSONOutput() sqltype.OutputEnvironment
	FunctionStringOutput() sqltype.OutputEnvironment
	FunctionFormatOutput() sqltype.OutputEnvironment
	FunctionJSONBRecordInput() sqltype.InputEnvironment

	// Argument and property inspection. These answer analysis-shaped
	// questions about child expressions without exposing engine node types.
	FunctionLiteralNull(BoundExpr) bool
	FunctionOverlapsConstant(BoundExpr) bool
	FunctionSleepArgumentPresent(BoundExpr) bool
	WholeRowNullPresence(BoundExpr) (handled, absent bool, err error)
	FunctionPropertiesMatch(ExprProperties, functions.IntrinsicPropertyRule) bool

	// RNG and entropy. Random owners borrow the session generator for one
	// operation; the UUID clock is session-owned; entropy is cryptographic.
	FunctionRandomOwner() (functions.RandomOwner, error)
	FunctionArrayRandomOwner() (functions.RandomOwner, error)
	FunctionUUIDReady() error
	FunctionUUIDTime() int64
	FunctionUUIDEntropy([]byte) error
	FunctionPgcryptoEntropy([]byte) error
	FunctionPgcryptoFIPSMode() bool

	// Clocks and statement time.
	FunctionStatementTime() (time.Time, error)
	FunctionWallTime() time.Time
	FunctionTimeOfDayZone() *sqltype.Zone
	FunctionSleepClock(sqltype.Usage) (sqltype.Value, error)

	// FunctionRegexExecution returns the statement's regex execution cache.
	FunctionRegexExecution() *sqltype.PGRegexExecution

	// FunctionBinaryConversion returns the selected encoding conversion
	// handler, or nil when none applies.
	FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error)
}

var _ ExprContext = (*EvalContext)(nil)

// evalContext recovers the engine evaluation context. A nil or foreign context
// yields nil, so the nodes' existing missing-context checks apply unchanged.
func evalContext(ctx ExprContext) *EvalContext {
	c, _ := ctx.(*EvalContext)
	return c
}
