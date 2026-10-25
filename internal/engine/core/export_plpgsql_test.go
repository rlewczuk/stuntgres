package core

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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

// FormatPLDiagnosticIdentity calls formatPLDiagnosticIdentity.
func FormatPLDiagnosticIdentity(read catalog.Read, reader catalog.DefinitionReader, path []ids.ObjectAddress, d *catalog.Procedure) string {
	return formatPLDiagnosticIdentity(read, reader, path, d)
}

// PLTriggerRelation calls plTriggerRelation.
func PLTriggerRelation(read catalog.Read, relation *catalog.Relation) (proc.PLTriggerRelation, error) {
	return plTriggerRelation(read, relation)
}

// ScopeAssignPLTarget calls x.assignPLTarget.
func ScopeAssignPLTarget(x *StatementScope, f *proc.PLFrame, checked proc.PLTarget, raw parser.Expr, query *parser.Select, read catalog.Read, origin plNoticeOrigin) (sqltype.Value, error) {
	return x.assignPLTarget(f, checked, raw, query, read, origin)
}

// ScopeConvertPLValue calls x.convertPLValue.
func ScopeConvertPLValue(x *StatementScope, value sqltype.Value, target sqltype.Usage) (sqltype.Value, error) {
	return x.convertPLValue(value, target)
}

// ScopePLRoutineDiagnosticIdentity calls x.plRoutineDiagnosticIdentity.
func ScopePLRoutineDiagnosticIdentity(x *StatementScope, read catalog.Read, d *catalog.Procedure) string {
	return x.plRoutineDiagnosticIdentity(read, d)
}

// ScopePLRuntimeError calls x.plRuntimeError.
func ScopePLRuntimeError(x *StatementScope, err error, read catalog.Read, d *catalog.Procedure, start parser.Position, end parser.Position, origin parser.Position, action string, expression bool) error {
	return x.plRuntimeError(err, read, d, start, end, origin, action, expression)
}
