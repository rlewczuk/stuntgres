package expr

import (
	"context"
	random "math/rand/v2"
	"time"

	"stuntgres/internal/sqltype"
)

// Context is the evaluation runtime visible to expression nodes outside the
// engine's own node set. The engine's *EvalContext is the only
// implementation; engine nodes recover its node runtime capability and keep
// engine-private state behind it.
//
// Every method is a per-evaluation capability of the current statement. None
// may be backed by package-level state. Evaluation always receives a non-nil
// Context; nodes may call it without a nil check.
type Context interface {
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
	FunctionLiteralNull(Expr) bool
	FunctionOverlapsConstant(Expr) bool
	FunctionSleepArgumentPresent(Expr) bool
	WholeRowNullPresence(Expr) (handled, absent bool, err error)
	FunctionPropertiesMatch(Properties, IntrinsicPropertyRule) bool

	// RNG and entropy. Random owners borrow the session generator for one
	// operation; the UUID clock is session-owned; entropy is cryptographic.
	FunctionRandomOwner() (RandomOwner, error)
	FunctionArrayRandomOwner() (RandomOwner, error)
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

// RandomOwner borrows the existing session's generator for one whole operation.
// Borrow unlocks on failure; only a successful borrow requires Release.
type RandomOwner interface {
	SeedWords(uint64, uint64)
	Borrow() (*random.Rand, error)
	Release()
}
