package engine

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ClosePLCapture calls closePLCapture.
func ClosePLCapture(result Result, primary error, format func(error) error) error {
	return closePLCapture(result, primary, format)
}

// DemandPLCapture calls demandPLCapture.
func DemandPLCapture(ctx context.Context, rows plCaptureRows, columns plColumns, exact bool) ([]sqltype.Value, bool, bool, error) {
	return demandPLCapture(ctx, rows, columns, exact)
}

// PLColumns names plColumns.
type PLColumns = plColumns

// PLHost names plHost.
type PLHost = plHost

// PLHostBeginQueryRows calls x.beginQueryRows.
func PLHostBeginQueryRows(x *plHost, f *proc.PLFrame, in proc.PLInstruction) *plQueryRows {
	return x.beginQueryRows(f, in)
}

// ScopeAppendPLNoticeContext calls x.appendPLNoticeContext.
func ScopeAppendPLNoticeContext(x *StatementScope, start int, origin plNoticeOrigin) {
	x.appendPLNoticeContext(start, origin)
}

// ScopeAssignPLCursorTargets calls x.assignPLCursorTargets.
func ScopeAssignPLCursorTargets(x *StatementScope, run *proc.PLRun, f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, fields []sqltype.Value, read catalog.Read) error {
	return x.assignPLCursorTargets(run, f, targets, cursor, fields, read)
}

// ScopeBeginPLException calls x.beginPLException.
func ScopeBeginPLException(x *StatementScope) (*plExceptionCheckpoint, error) {
	return x.beginPLException()
}

// ScopeEndPLException calls x.endPLException.
func ScopeEndPLException(x *StatementScope, c *plExceptionCheckpoint, rollback bool, primary error) error {
	return x.endPLException(c, rollback, primary)
}

// ScopeEvaluatePLExpression calls x.evaluatePLExpression.
func ScopeEvaluatePLExpression(x *StatementScope, f *proc.PLFrame, statement *parser.Select, read catalog.Read, assignment *analyze.PLAssignment, origin plNoticeOrigin) (sqltype.Value, sqltype.Usage, bool, error) {
	return x.evaluatePLExpression(f, statement, read, assignment, origin)
}

// ScopeInvokePLRoutine calls x.invokePLRoutine.
func ScopeInvokePLRoutine(x *StatementScope, d *catalog.Procedure, input *proc.Frame, read catalog.Read, collation sqltype.CollationRef) (sqltype.Value, error) {
	return x.invokePLRoutine(d, input, read, collation)
}

// ScopeMovePLCursorPortal calls x.movePLCursorPortal.
func ScopeMovePLCursorPortal(x *StatementScope, run *proc.PLRun, f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, motion *parser.FetchCursor, read catalog.Read) error {
	return x.movePLCursorPortal(run, f, targets, cursor, motion, read)
}

// ScopeNewPLRun calls x.newPLRun.
func ScopeNewPLRun(x *StatementScope, d *catalog.Procedure, f *proc.PLFrame, read catalog.Read) *proc.PLRun {
	return x.newPLRun(d, f, read)
}

// ScopePreparePLTargetValue calls x.preparePLTargetValue.
func ScopePreparePLTargetValue(x *StatementScope, run *proc.PLRun, f *proc.PLFrame, checked proc.PLTarget, value sqltype.Value, current sqltype.Value, read catalog.Read) (sqltype.Value, error) {
	return x.preparePLTargetValue(run, f, checked, value, current, read)
}

// PLExceptionCheckpointSerial returns x.serial.
func PLExceptionCheckpointSerial(x *plExceptionCheckpoint) uint64 { return x.serial }

// PLNoticeOriginFields holds the plNoticeOrigin fields tests set.
type PLNoticeOriginFields struct {
	Position parser.Position
	Action   string
}

// NewPLNoticeOrigin builds a plNoticeOrigin from f.
func NewPLNoticeOrigin(f PLNoticeOriginFields) plNoticeOrigin {
	return plNoticeOrigin{position: f.Position, action: f.Action}
}

// PLQueryRowsResultRef returns &x.result.
func PLQueryRowsResultRef(x *plQueryRows) *Result { return &x.result }

// PLQueryRowsSchemaRef returns &x.schema.
func PLQueryRowsSchemaRef(x *plQueryRows) *[]catalog.Column { return &x.schema }
