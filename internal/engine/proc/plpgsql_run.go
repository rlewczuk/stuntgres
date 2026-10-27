package proc

import (
	"context"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PLOrigin names the statement whose notices a host call finalizes. The zero
// origin adds no notice context; callers that bracket notices themselves use it.
type PLOrigin struct {
	Position parser.Position
	Action   string
}

// PLCheckpoint is the host's opaque exception boundary. Only the host that
// created it recovers its concrete type.
type PLCheckpoint any

// PLColumns describes a row producer's output columns.
type PLColumns interface {
	Width() int
	Column(i int) (name string, usage sqltype.Usage)
}

// PLRows is an open query-FOR producer. Next fills dst, which has Width()
// elements. Close releases the producers, formats their failures and ends the
// loop's activation; it receives the walk's primary error and returns the final
// one. Close is called exactly once.
type PLRows interface {
	PLColumns
	Next(ctx context.Context, dst []sqltype.Value) (bool, error)
	Close(primary error) error
}

// PLHost is the walk's only capability. Every method takes values and runs with
// nothing held by the walk; the host takes its own locks.
type PLHost interface {
	// Context is the admitted operation context, checked per statement and
	// per loop iteration.
	Context() context.Context
	// Identity renders only requested diagnostic context, never ordinary statement work.
	Identity() string
	// EnterRoutine/LeaveRoutine balance live invocation provenance, not lexical SQL scratch.
	EnterRoutine()
	LeaveRoutine()
	Diagnostics(f *PLFrame, in PLInstruction) error
	LookupType(id sqltype.ID) (*sqltype.Descriptor, bool)
	// TypeName uses the current catalog visibility for an error-only type name.
	TypeName(typ *sqltype.Descriptor) string
	// Evaluate runs a single-target expression query and returns its value and
	// usage; no row yields a typed NULL.
	Evaluate(f *PLFrame, q *parser.Select, at PLOrigin) (sqltype.Value, sqltype.Usage, error)
	// Convert applies PL assignment conversion to target.
	Convert(v sqltype.Value, target sqltype.Usage, at PLOrigin) (sqltype.Value, error)
	// AssignTarget evaluates and publishes a field or subscript assignment.
	AssignTarget(f *PLFrame, t PLTarget, raw parser.Expr, q *parser.Select, at PLOrigin) error
	// TargetUsage resolves a reached partial destination without evaluating bounds.
	TargetUsage(f *PLFrame, t PLTarget) (sqltype.Usage, error)
	// AssignValue publishes one already-evaluated partial destination. Whole
	// slots use AssignVariable directly; lists call this sequentially.
	AssignValue(f *PLFrame, t PLTarget, value sqltype.Value, at PLOrigin) error
	BeginException() (PLCheckpoint, error)
	// EndException releases (rollback false) or rolls back the boundary.
	EndException(c PLCheckpoint, rollback bool, primary error) error
	// OpenQuery opens a static query-FOR producer; its errors are already formatted.
	OpenQuery(f *PLFrame, in PLInstruction) (PLRows, error)
	Capture(r *PLRun, f *PLFrame, in PLInstruction) error
	// Command executes static SQL, PERFORM or CALL with its own result policy.
	Command(r *PLRun, f *PLFrame, in PLInstruction) error
	// Dynamic evaluates invocation-local SQL and owns its result policy.
	Dynamic(r *PLRun, f *PLFrame, in PLInstruction) error
	// Cursor runs a cursor instruction. Cursor-FOR re-enters r.Block.
	Cursor(r *PLRun, f *PLFrame, in PLInstruction) (sqltype.Value, PLFlow, error)
	// Message runs RAISE or ASSERT.
	Message(f *PLFrame, in PLInstruction) error
	Transaction(in PLInstruction) error
}

// PLRun is one PL invocation's walk over a checked program.
type PLRun struct {
	Host       PLHost
	Definition *Definition
	Frame      *PLFrame
	Values     *[]sqltype.Value
}

func plError(code, message string) *sqlstate.Error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
