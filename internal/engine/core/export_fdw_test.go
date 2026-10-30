package core

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// ScopeEnlistForeign calls x.enlistForeign.
func ScopeEnlistForeign(x *StatementScope, ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	return x.enlistForeign(ctx, wrapper, request, writing)
}

// ScopeForeignTablesOrZero calls x.foreignTablesOrZero.
func ScopeForeignTablesOrZero(x *StatementScope) foreignTableScope {
	return x.foreignTablesOrZero()
}

// ForeignTableScopeOwner returns x.owner.
func ForeignTableScopeOwner(x foreignTableScope) *exec.ForeignScanOwner { return x.owner }

// SetForeignTableScopeOwner sets x.owner.
func SetForeignTableScopeOwner(x *foreignTableScope, v *exec.ForeignScanOwner) { x.owner = v }

// ForeignTableScopeCursor returns x.cursor.
func ForeignTableScopeCursor(x foreignTableScope) *sqlCursor { return x.cursor }

// CommitForeign calls commitForeign.
func CommitForeign(t *session.TransactionOwner, ctx context.Context, ref ids.TxRef) error {
	return commitForeign(t, ctx, ref)
}

// ForeignSavepoint calls foreignSavepoint.
func ForeignSavepoint(t *session.TransactionOwner, ctx context.Context, kind int, id uint64) error {
	return foreignSavepoint(t, ctx, kind, id)
}

// ScopeDiscardForeignHoldPromotions calls x.discardForeignHoldPromotions.
func ScopeDiscardForeignHoldPromotions(x *StatementScope, primary error) error {
	return x.discardForeignHoldPromotions(primary)
}

// ScopeFinishForeignModifications calls x.finishForeignModifications.
func ScopeFinishForeignModifications(x *StatementScope, primary error) error {
	return x.finishForeignModifications(primary)
}

// ScopeFinishForeignStatements calls x.finishForeignStatements.
func ScopeFinishForeignStatements(x *StatementScope, primary error) error {
	return x.finishForeignStatements(primary)
}

// ScopeForeignTables calls x.foreignTables.
func ScopeForeignTables(x *StatementScope) *foreignTableScope {
	return x.foreignTables()
}

// ScopeForeignUnlocked calls x.foreignUnlocked.
func ScopeForeignUnlocked(x *StatementScope) {
	x.foreignUnlocked()
}

// ForeignTableScopeHoldPromotions returns x.holdPromotions.
func ForeignTableScopeHoldPromotions(x foreignTableScope) []*sqlCursor { return x.holdPromotions }

// ForeignTableScopeStatements returns x.statements.
func ForeignTableScopeStatements(x *foreignTableScope) []*fdw.Participant { return x.statements }

// ForeignTableScopeValueStatements returns x.statements.
func ForeignTableScopeValueStatements(x foreignTableScope) []*fdw.Participant { return x.statements }

// SetForeignTableScopeStatements sets x.statements.
func SetForeignTableScopeStatements(x *foreignTableScope, v []*fdw.Participant) { x.statements = v }

var ForeignValidatorFrame = foreignValidatorFrame

func ScopePrepareForeignValidator(s *StatementScope, p *catalog.Procedure, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (*proc.Frame, sqltype.Usage, error) {
	return s.prepareForeignValidator(p, kind, options)
}

func ScopeInvokeFDWValidator(s *StatementScope, p *catalog.Procedure, frame *proc.Frame, result sqltype.Usage) error {
	return s.invokeFDWValidator(p, frame, result)
}

func ScopePendingForeignConstructions(s *StatementScope) int {
	return len(s.foreignConstructions)
}

func ForeignConstructionRetainsState(t *ForeignConstruction) bool {
	return t.scope != nil || t.expected != nil || t.foreign != nil || t.columns != nil
}

// ExplainBoundForTest renders the actual retained binding under the ordinary
// renderer. Enter and leave with Mu held, as at the production command boundary.
func ExplainBoundForTest(scope *StatementScope, auth AuthorizationView, statement parser.Statement, binding analyze.Binding) ([]string, error) {
	r, err := newExplainRenderer(scope, auth, explainOptions{verbose: true})
	if err != nil { return nil, err }
	_, r.exportValue, _ = scope.Engine().Foreign.Codecs()
	return r.command(statement, explainBound{Binding: binding})
}
