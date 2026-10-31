package core

import (
	"context"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DisposeSQLCursors calls disposeSQLCursors.
func DisposeSQLCursors(ctx context.Context, cursors []*sqlCursor) error {
	return disposeSQLCursors(ctx, cursors)
}

// SQLCursor names sqlCursor.
type SQLCursor = sqlCursor

// ValidateSQLRoutineResult calls validateSQLRoutineResult.
func ValidateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage) (proc.ResultPlan, error) {
	return validateSQLRoutineResult(columns, result)
}

// ScopeCloseCursor calls x.closeCursor.
func ScopeCloseCursor(x *StatementScope, close *parser.CloseCursor) (Result, error) {
	return x.closeCursor(close)
}

// ScopeExecuteFetchCursor calls x.executeFetchCursor.
func ScopeExecuteFetchCursor(x *StatementScope, fetch *parser.FetchCursor, count int) (Result, error) {
	return x.executeFetchCursor(fetch, count)
}

// ScopeFetchCursor calls x.fetchCursor.
func ScopeFetchCursor(x *StatementScope, fetch *parser.FetchCursor) (Result, error) {
	return x.fetchCursor(fetch)
}

// ScopeFetchCursorPortal calls x.fetchCursorPortal.
func ScopeFetchCursorPortal(x *StatementScope, c *sqlCursor, fetch *parser.FetchCursor) (Result, error) {
	return x.fetchCursorPortal(c, fetch)
}

// ScopeInstallCursor calls x.installCursor.
func ScopeInstallCursor(x *StatementScope, declaration *parser.DeclareCursor, requestedName *string, root plan.Node, source *exec.Source, parameters []sqltype.Value, scroll bool) (*sqlCursor, string, error) {
	return x.installCursor(declaration, requestedName, root, source, parameters, scroll)
}

// ScopeResolveCursorPortal calls x.resolveCursorPortal.
func ScopeResolveCursorPortal(x *StatementScope, name string) (*sqlCursor, error) {
	return x.resolveCursorPortal(name)
}

// CursorsClear calls x.clear.
func CursorsClear(x *cursorRegistry) []*sqlCursor {
	return x.clear()
}

// CursorsEndTransaction calls x.endTransaction.
func CursorsEndTransaction(x *cursorRegistry, ref ids.TxRef) []*sqlCursor {
	return x.endTransaction(ref)
}

// CursorsOrdinal returns x.ordinal.
func CursorsOrdinal(x *cursorRegistry) uint64 { return x.ordinal }

// CursorsToken returns x.token.
func CursorsToken(x *cursorRegistry) ids.SessionToken { return x.token }

// SetCursorsOrdinal sets x.ordinal.
func SetCursorsOrdinal(x *cursorRegistry, v uint64) { x.ordinal = v }

// CursorAuthority returns x.authority.
func CursorAuthority(x *sqlCursor) *exec.Authority { return x.authority }

// CursorCatalog returns x.catalog.
func CursorCatalog(x *sqlCursor) catalog.Read { return x.catalog }

// CursorCount returns x.count.
func CursorCount(x *sqlCursor) int { return x.count }

// CursorCreated returns x.created.
func CursorCreated(x *sqlCursor) sqltype.Value { return x.created }

// CursorDispose calls x.dispose.
func CursorDispose(x *sqlCursor, ctx context.Context) error {
	return x.dispose(ctx)
}

// CursorEpoch returns x.epoch.
func CursorEpoch(x *sqlCursor) ids.CatalogEpoch { return x.epoch }

// CursorHold returns x.hold.
func CursorHold(x *sqlCursor) bool { return x.hold }

// CursorLeaves returns x.leaves.
func CursorLeaves(x *sqlCursor) []exec.Leaf { return x.leaves }

// CursorLocks returns x.locks.
func CursorLocks(x *sqlCursor) *exec.LockContext { return x.locks }

// CursorOrdinal returns x.ordinal.
func CursorOrdinal(x *sqlCursor) uint64 { return x.ordinal }

// CursorPinned returns x.pinned.
func CursorPinned(x *sqlCursor) bool { return x.pinned }

// CursorPromoted returns x.promoted.
func CursorPromoted(x *sqlCursor) bool { return x.promoted }

// CursorRecordShape returns x.recordShape.
func CursorRecordShape(x *sqlCursor) *sqltype.Descriptor { return x.recordShape }

// CursorRoot returns x.root.
func CursorRoot(x *sqlCursor) *plan.ProjectNode { return x.Root }

// CursorRoutineAuth returns x.routineAuth.
func CursorRoutineAuth(x *sqlCursor) AuthorizationView { return x.routineAuth }

// CursorRoutineAuthRef returns &x.routineAuth.
func CursorRoutineAuthRef(x *sqlCursor) *AuthorizationView { return &x.routineAuth }

// CursorRoutineDependencies returns x.routineDependencies.
func CursorRoutineDependencies(x *sqlCursor) []catalog.ExpressionDependency {
	return x.routineDependencies
}

// CursorSchema returns x.schema.
func CursorSchema(x *sqlCursor) []catalog.Column { return x.schema }

// CursorScroll returns x.scroll.
func CursorScroll(x *sqlCursor) bool { return x.scroll }

// CursorState returns x.state.
func CursorState(x *sqlCursor) exec.State { return x.state }

// CursorStatement returns x.statement.
func CursorStatement(x *sqlCursor) string { return x.statement }

// CursorStoreOffset returns x.storeOffset.
func CursorStoreOffset(x *sqlCursor) int64 { return x.storeOffset }

// CursorTx returns x.tx.
func CursorTx(x *sqlCursor) ids.TxRef { return x.tx }

// CursorValues returns x.values.
func CursorValues(x *sqlCursor) []sqltype.Value { return x.values }

// SetCursorPinned sets x.pinned.
func SetCursorPinned(x *sqlCursor, v bool) { x.pinned = v }

// SetCursorRoutineDependencies sets x.routineDependencies.
func SetCursorRoutineDependencies(x *sqlCursor, v []catalog.ExpressionDependency) {
	x.routineDependencies = v
}

// SetCursorStage sets x.stage.
func SetCursorStage(x *sqlCursor, v *exec.Stage) { x.stage = v }

// SetCursorTx sets x.tx.
func SetCursorTx(x *sqlCursor, v ids.TxRef) { x.tx = v }

// EngineHoldPrecommit calls x.holdPrecommit.
func EngineHoldPrecommit(x *Engine, op *session.Operation, owner *session.TransactionOwner, start time.Time, settlement *StatementScope) error {
	return x.holdPrecommit(op, owner, start, settlement)
}

// ScopeInstallSQLCursor calls x.installSQLCursor.
func ScopeInstallSQLCursor(x *StatementScope, declaration *parser.DeclareCursor, root plan.Node, source *exec.Source, parameters []sqltype.Value) (Result, error) {
	return x.installSQLCursor(declaration, root, source, parameters)
}

// CursorsRollback calls x.rollback.
func CursorsRollback(x *cursorRegistry, mark uint64) []*sqlCursor {
	return x.rollback(mark)
}

// CursorForeignOwner returns x.foreignOwner.
func CursorForeignOwner(x *sqlCursor) *exec.ForeignScanOwner { return x.foreignOwner }

// CursorRLSPrograms returns x.rlsPrograms.
func CursorRLSPrograms(x *sqlCursor) []*rowSecurityProgram { return x.rlsPrograms }

// CursorReleaseProducer calls x.releaseProducer.
func CursorReleaseProducer(x *sqlCursor, ctx context.Context) error {
	return x.releaseProducer(ctx)
}

// CursorFields holds the sqlCursor fields tests set.
type CursorFields struct {
	Tx           ids.TxRef
	Ordinal      uint64
	Epoch        ids.CatalogEpoch
	Hold         bool
	Promoted     bool
	Pinned       bool
	Values       []sqltype.Value
	Count        int
	Root         *plan.ProjectNode
	Schema       []catalog.Column
	Source       *exec.Source
	State        exec.State
	Stage        *exec.Stage
	Authority    *exec.Authority
	ForeignOwner *exec.ForeignScanOwner
	Scroll       bool
}

// NewCursor builds a sqlCursor from f.
func NewCursor(f CursorFields) *sqlCursor {
	return &sqlCursor{tx: f.Tx, ordinal: f.Ordinal, epoch: f.Epoch, hold: f.Hold, promoted: f.Promoted, pinned: f.Pinned, values: f.Values, count: f.Count, PreparedResource: PreparedResource{Root: f.Root}, schema: f.Schema, Source: f.Source, state: f.State, stage: f.Stage, authority: f.Authority, foreignOwner: f.ForeignOwner, scroll: f.Scroll}
}

// CursorStage returns x.stage.
func CursorStage(x *sqlCursor) *exec.Stage { return x.stage }

// CursorExecutionState borrows the cursor's actual retained executor owner.
func CursorExecutionState(c *sqlCursor) *exec.State { return &c.state }
