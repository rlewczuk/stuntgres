package core

import (
	"context"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// ScopeAcquireAdvisory calls x.acquireAdvisory.
func ScopeAcquireAdvisory(x *StatementScope, ctx context.Context, ref lock.Resource, mode lock.Mode, lifetime functions.AdvisoryLifetime, try bool) (bool, error) {
	return x.acquireAdvisory(ctx, ref, mode, lifetime, try)
}

// ScopeActiveSQLRoutineDepth returns x.activeSQLRoutineDepth.
func ScopeActiveSQLRoutineDepth(x *StatementScope) int { return x.activeSQLRoutineDepth }

// ScopeAuthority returns x.authority.
func ScopeAuthority(x *StatementScope) *exec.Authority { return x.authority }

// ScopeCTEInvocation returns x.cteInvocation.
func ScopeCTEInvocation(x *StatementScope) *exec.CTEInvocation { return x.cteInvocation }

// ScopeCurrentSnapshot returns x.currentSnapshot.
func ScopeCurrentSnapshot(x *StatementScope) *sqltype.Snapshot { return x.currentSnapshot }

// ScopeEffectCommand returns x.effectCommand.
func ScopeEffectCommand(x *StatementScope) uint64 { return x.effectCommand }

// ScopeEffectSerial returns x.effectSerial.
func ScopeEffectSerial(x *StatementScope) uint64 { return x.effectSerial }

// ScopeForeignActionsSlot returns x.foreignActionsSlot.
func ScopeForeignActionsSlot(x *StatementScope) *dml.ForeignActionScope { return x.foreignActionsSlot }

// ScopeImplicit returns x.implicit.
func ScopeImplicit(x *StatementScope) bool { return x.implicit }

// ScopeIndexesSlot returns x.indexesSlot.
func ScopeIndexesSlot(x *StatementScope) *index.Scope { return x.indexesSlot }

// ScopeLease returns x.lease.
func ScopeLease(x *StatementScope) session.StatementLease { return x.lease }

// ScopeLeaseRef returns &x.lease.
func ScopeLeaseRef(x *StatementScope) *session.StatementLease { return &x.lease }

// ScopeMessage returns x.message.
func ScopeMessage(x *StatementScope) bool { return x.message }

// ScopeMultiEvents returns x.multiEvents.
func ScopeMultiEvents(x *StatementScope) []lock.MultiEvent { return x.multiEvents }

// ScopeMutations returns x.mutations.
func ScopeMutations(x *StatementScope) []StatementMutation { return x.mutations }

// ScopeNotificationEvents returns x.notificationEvents.
func ScopeNotificationEvents(x *StatementScope) []session.Notification { return x.notificationEvents }

// ScopeRoutinePrograms returns x.routinePrograms.
func ScopeRoutinePrograms(x *StatementScope) []commandRoutineProgram { return x.routinePrograms }

// ScopeSQLRoutine returns x.sqlRoutine.
func ScopeSQLRoutine(x *StatementScope) sqlRoutineDemand { return x.sqlRoutine }

// ScopeSQLRoutineRef returns &x.sqlRoutine.
func ScopeSQLRoutineRef(x *StatementScope) *sqlRoutineDemand { return &x.sqlRoutine }

// ScopeSessionCompleted returns x.sessionCompleted.
func ScopeSessionCompleted(x *StatementScope) bool { return x.sessionCompleted }

// ScopeSessionCompletedRef returns &x.sessionCompleted.
func ScopeSessionCompletedRef(x *StatementScope) *bool { return &x.sessionCompleted }

// ScopeSnapshotFrontier returns x.snapshotFrontier.
func ScopeSnapshotFrontier(x *StatementScope) ids.CommitSeq { return x.snapshotFrontier }

// ScopeStatementStart returns x.statementStart.
func ScopeStatementStart(x *StatementScope) time.Time { return x.statementStart }

// ScopeTransactionStart returns x.transactionStart.
func ScopeTransactionStart(x *StatementScope) time.Time { return x.transactionStart }

// SetScopeAuthority sets x.authority.
func SetScopeAuthority(x *StatementScope, v *exec.Authority) { x.authority = v }

// SetScopeEffectCommand sets x.effectCommand.
func SetScopeEffectCommand(x *StatementScope, v uint64) { x.effectCommand = v }

// SetScopeEffectSerial sets x.effectSerial.
func SetScopeEffectSerial(x *StatementScope, v uint64) { x.effectSerial = v }

// SetScopeForeignActionsSlot sets x.foreignActionsSlot.
func SetScopeForeignActionsSlot(x *StatementScope, v *dml.ForeignActionScope) {
	x.foreignActionsSlot = v
}

// SetScopeIndexesSlot sets x.indexesSlot.
func SetScopeIndexesSlot(x *StatementScope, v *index.Scope) { x.indexesSlot = v }

// SetScopeLargeObjectRead sets x.largeObjectRead.
func SetScopeLargeObjectRead(x *StatementScope, v catalog.Read) { x.largeObjectRead = v }

// SetScopeMultiEvents sets x.multiEvents.
func SetScopeMultiEvents(x *StatementScope, v []lock.MultiEvent) { x.multiEvents = v }

// SetScopeNotificationEvents sets x.notificationEvents.
func SetScopeNotificationEvents(x *StatementScope, v []session.Notification) {
	x.notificationEvents = v
}

// SetScopeSelf sets x.self.
func SetScopeSelf(x *StatementScope, v *StatementScope) { x.self = v }

// ScopeFields holds the StatementScope fields tests set.
type ScopeFields struct {
	Owner                 *session.TransactionOwner
	Op                    *session.Operation
	NotificationActions   []session.NotificationAction
	NotificationEvents    []session.Notification
	Expected              *Description
	StatementStart        time.Time
	TransactionStart      time.Time
	ActiveSQLRoutineDepth int
	Catalog               *session.Journal
	LargeObjectRead       catalog.Read
	SQLRoutine            sqlRoutineDemand
}

// NewScope builds a StatementScope from f.
func NewScope(f ScopeFields) *StatementScope {
	return &StatementScope{Owner: f.Owner, Op: f.Op, notificationActions: f.NotificationActions, notificationEvents: f.NotificationEvents, Expected: f.Expected, statementStart: f.StatementStart, transactionStart: f.TransactionStart, activeSQLRoutineDepth: f.ActiveSQLRoutineDepth, Catalog: f.Catalog, largeObjectRead: f.LargeObjectRead, sqlRoutine: f.SQLRoutine}
}

// ScopeLargeObjectRead returns x.largeObjectRead.
func ScopeLargeObjectRead(x *StatementScope) catalog.Read { return x.largeObjectRead }

// ScopeRLSActive returns x.rlsActive.
func ScopeRLSActive(x *StatementScope) []ids.ObjectAddress { return x.rlsActive }

// ScopeRLSAdmissionDepth returns x.rlsAdmissionDepth.
func ScopeRLSAdmissionDepth(x *StatementScope) int { return x.rlsAdmissionDepth }

// ScopeRLSAdmissionDepthRef returns &x.rlsAdmissionDepth.
func ScopeRLSAdmissionDepthRef(x *StatementScope) *int { return &x.rlsAdmissionDepth }

// ScopeRLSCommand returns x.rlsCommand.
func ScopeRLSCommand(x *StatementScope) *preparedCommandExecution { return x.rlsCommand }

// ScopeRLSCommandRef returns &x.rlsCommand.
func ScopeRLSCommandRef(x *StatementScope) **preparedCommandExecution { return &x.rlsCommand }

// ScopeRLSDataRefs returns x.rlsDataRefs.
func ScopeRLSDataRefs(x *StatementScope) []ids.ObjectRef { return x.rlsDataRefs }

// ScopeRLSImportWaits returns x.rlsImportWaits.
func ScopeRLSImportWaits(x *StatementScope) uint64 { return x.rlsImportWaits }

// ScopeRLSImportWaitsRef returns &x.rlsImportWaits.
func ScopeRLSImportWaitsRef(x *StatementScope) *uint64 { return &x.rlsImportWaits }

// ScopeRLSPrograms returns x.rlsPrograms.
func ScopeRLSPrograms(x *StatementScope) []*rowSecurityProgram { return x.rlsPrograms }

// SetScopeActiveSQLRoutineDepth sets x.activeSQLRoutineDepth.
func SetScopeActiveSQLRoutineDepth(x *StatementScope, v int) { x.activeSQLRoutineDepth = v }

// SetScopeRLSCommand sets x.rlsCommand.
func SetScopeRLSCommand(x *StatementScope, v *preparedCommandExecution) { x.rlsCommand = v }

// SetScopeRLSDataRefs sets x.rlsDataRefs.
func SetScopeRLSDataRefs(x *StatementScope, v []ids.ObjectRef) { x.rlsDataRefs = v }

// SetScopeRLSPrograms sets x.rlsPrograms.
func SetScopeRLSPrograms(x *StatementScope, v []*rowSecurityProgram) { x.rlsPrograms = v }

// SetWriteStageScope sets x.scope.
func SetWriteStageScope(x *WriteStage, v *StatementScope) { x.scope = v }

// SetWriteStageSelf sets x.self.
func SetWriteStageSelf(x *WriteStage, v *WriteStage) { x.self = v }

// WriteStageScope returns x.scope.
func WriteStageScope(x *WriteStage) *StatementScope { return x.scope }

// WriteStageSelf returns x.self.
func WriteStageSelf(x *WriteStage) *WriteStage { return x.self }
