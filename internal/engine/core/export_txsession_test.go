package core

import (
	"context"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// AppendBlockingPid calls appendBlockingPid.
func AppendBlockingPid(values []sqltype.Value, typ *sqltype.Descriptor, token ids.SessionToken, prepared bool, domain *ids.Domain) ([]sqltype.Value, error) {
	return appendBlockingPid(values, typ, token, prepared, domain)
}

// DrainCursorCleanup calls drainCursorCleanup.
func DrainCursorCleanup(t *session.TransactionOwner, ctx context.Context, scope *StatementScope) error {
	return drainCursorCleanup(t, ctx, scope)
}

// EndTransactionSession calls endTransactionSession.
func EndTransactionSession(t *session.TransactionOwner, token ids.SessionToken) error {
	return endTransactionSession(t, token)
}

// NotificationEffects calls notificationEffects.
func NotificationEffects(op *session.Operation, ref ids.TxRef, phase *session.PreparedPublication, scope *StatementScope) ([]session.NotificationAction, []session.Notification, error) {
	return notificationEffects(op, ref, phase, scope)
}

// NotificationSessionEnd names notificationSessionEnd.
type NotificationSessionEnd = notificationSessionEnd

// OwnerExtension names ownerExtension.
type OwnerExtension = ownerExtension

// OwnerExtensionOf calls ownerExtensionOf.
func OwnerExtensionOf(t *session.TransactionOwner) *ownerExtension {
	return ownerExtensionOf(t)
}

// OwnerExtensionOrZero calls ownerExtensionOrZero.
func OwnerExtensionOrZero(t *session.TransactionOwner) ownerExtension {
	return ownerExtensionOrZero(t)
}

// SessionHost names sessionHost.
type SessionHost = sessionHost

// TemporaryNamespace calls temporaryNamespace.
func TemporaryNamespace(auth AuthorizationView) ids.ObjectAddress {
	return temporaryNamespace(auth)
}

// TemporarySessionIndexes names temporarySessionIndexes.
type TemporarySessionIndexes = temporarySessionIndexes

// EngineApplySetting calls x.applySetting.
func EngineApplySetting(x *Engine, op *session.Operation, owner *session.SettingsStore, d settings.Definition, input *string, local bool) (string, error) {
	return x.applySetting(op.Context(), op, owner, d, input, session.SettingAssignment(local), "")
}

// EngineBeginAdmitted calls x.beginAdmitted.
func EngineBeginAdmitted(x *Engine, op *session.Operation, t *session.TransactionOwner, start time.Time) (ids.TxRef, error) {
	return x.beginAdmitted(op, t, start)
}

// EngineBlockingPidValues calls x.blockingPidValues.
func EngineBlockingPidValues(x *Engine, pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	return x.blockingPidValues(pid, typ)
}

// EngineCaptureVisibilitySnapshotLocked calls x.captureVisibilitySnapshotLocked.
func EngineCaptureVisibilitySnapshotLocked(x *Engine, scope *StatementScope) error {
	return x.captureVisibilitySnapshotLocked(scope)
}

// EngineCompletePrepared calls x.completePrepared.
func EngineCompletePrepared(x *Engine, op *session.Operation, gid string, commit bool) error {
	return x.completePrepared(op, gid, commit)
}

// EngineEndAdmitted calls x.endAdmitted.
func EngineEndAdmitted(x *Engine, op *session.Operation, t *session.TransactionOwner, ref ids.TxRef, commit bool, chain bool, sqlEnd bool, nextStart time.Time, scope *StatementScope) (EndResult, error) {
	return x.endAdmitted(op, t, ref, commit, sqlRestart(chain), sqlEnd, nextStart, scope)
}

// EngineEndSerializable calls x.endSerializable.
func EngineEndSerializable(x *Engine, ref ids.TxRef, committed bool) {
	x.endSerializable(ref, committed)
}

// EngineEndSerializableLocked calls x.endSerializableLocked.
func EngineEndSerializableLocked(x *Engine, ref ids.TxRef, committed bool) {
	x.endSerializableLocked(ref, committed)
}

// EngineEndTemporarySession calls x.endTemporarySession.
func EngineEndTemporarySession(x *Engine, s *session.Session) error {
	return x.endTemporarySession(s)
}

// EngineEnrollSerializableLocked calls x.enrollSerializableLocked.
func EngineEnrollSerializableLocked(x *Engine, ref ids.TxRef, isolation session.Isolation) {
	x.enrollSerializableLocked(ref, isolation)
}

// EngineExecuteSetting calls x.executeSetting.
func EngineExecuteSetting(x *Engine, op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	return x.executeSetting(op.Context(), op, stmt, count, implicit)
}

// EngineFenceSerializableLocked calls x.fenceSerializableLocked.
func EngineFenceSerializableLocked(x *Engine) {
	x.fenceSerializableLocked()
}

// EngineInstallTemporarySessionStageLocked calls x.installTemporarySessionStageLocked.
func EngineInstallTemporarySessionStageLocked(x *Engine, s *session.Session, stage *CatalogStage, namespaces []ids.ObjectAddress, indexes *temporarySessionIndexes) (bool, error) {
	return x.installTemporarySessionStageLocked(s, stage, namespaces, indexes)
}

// EngineNotificationPublicationUnchangedLocked calls x.notificationPublicationUnchangedLocked.
func EngineNotificationPublicationUnchangedLocked(x *Engine, p *notificationPublication) bool {
	return x.notificationPublicationUnchangedLocked(p)
}

// EnginePrepareNotificationPublication calls x.prepareNotificationPublication.
func EnginePrepareNotificationPublication(x *Engine, sender *session.Session, actions []session.NotificationAction, events []session.Notification) *notificationPublication {
	return x.prepareNotificationPublication(sender, actions, events)
}

// EnginePrepareTemporaryCommit calls x.prepareTemporaryCommit.
func EnginePrepareTemporaryCommit(x *Engine, op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal) (map[ids.ObjectAddress][]storage.RowVersion, map[ids.ObjectAddress]*visibility.PrivateDeletes, *session.Journal, error) {
	return x.prepareTemporaryCommit(op, ref, writes, deletes, c)
}

// EnginePrepareTemporarySessionIndexes calls x.prepareTemporarySessionIndexes.
func EnginePrepareTemporarySessionIndexes(x *Engine, stage *CatalogStage) (*temporarySessionIndexes, error) {
	return x.prepareTemporarySessionIndexes(stage)
}

// EnginePreserveTemporaryLocked calls x.preserveTemporaryLocked.
func EnginePreserveTemporaryLocked(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, restoreOp *session.Operation, indexes *index.Snapshot, principal catalog.ProcedureRemapper, oids *catalog.OIDReservation) error {
	return x.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes, principal, oids)
}

// EnginePublishRows calls x.publishRows.
func EnginePublishRows(x *Engine, op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *session.CatalogAuthority, scope *StatementScope) error {
	return x.publishRows(op, ref, writes, deletes, c, authority, scope)
}

// EnginePublishRowsPrepared calls x.publishRowsPrepared.
func EnginePublishRowsPrepared(x *Engine, op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *session.CatalogAuthority, phase *session.PreparedPublication, scope *StatementScope) error {
	return x.publishRowsPrepared(op, ref, writes, deletes, c, authority, phase, scope)
}

// EnginePublishWrites calls x.publishWrites.
func EnginePublishWrites(x *Engine, op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, scope *StatementScope) error {
	return x.publishWrites(op, ref, writes, deletes, scope)
}

// EngineReconcileSerializableWritesLocked calls x.reconcileSerializableWritesLocked.
func EngineReconcileSerializableWritesLocked(x *Engine, t *session.TransactionOwner) {
	x.reconcileSerializableWritesLocked(t)
}

// EngineSafeSnapshotBlockingPidsLocked calls x.safeSnapshotBlockingPidsLocked.
func EngineSafeSnapshotBlockingPidsLocked(x *Engine, pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	return x.safeSnapshotBlockingPidsLocked(pid, typ)
}

// EngineSerializableEligibleLocked calls x.serializableEligibleLocked.
func EngineSerializableEligibleLocked(x *Engine, ref ids.ObjectRef, changes *session.Journal) bool {
	return x.serializableEligibleLocked(ref, changes)
}

// EngineStartSession calls x.startSession.
func EngineStartSession(x *Engine, ctx context.Context, start session.SessionStart, private bool) (*session.Session, error) {
	return x.startSession(ctx, start, private)
}

// EngineTransactionAgeLocked calls x.transactionAgeLocked.
func EngineTransactionAgeLocked(x *Engine, ref ids.TxRef, xid uint32) (int32, error) {
	return x.transactionAgeLocked(ref, xid)
}

// ScopeAcquireAdvisory calls x.acquireAdvisory.
func ScopeAcquireAdvisory(x *StatementScope, ctx context.Context, ref lock.Resource, mode lock.Mode, lifetime functions.AdvisoryLifetime, try bool) (bool, error) {
	return x.acquireAdvisory(ctx, ref, mode, lifetime, try)
}

// ScopeActiveSQLRoutineDepth returns x.activeSQLRoutineDepth.
func ScopeActiveSQLRoutineDepth(x *StatementScope) int { return x.activeSQLRoutineDepth }

// ScopeAttachTransactionLocked calls x.attachTransactionLocked.
func ScopeAttachTransactionLocked(x *StatementScope) {
	x.attachTransactionLocked()
}

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

// ScopeExecuteControl calls x.executeControl.
func ScopeExecuteControl(x *StatementScope, control *parser.TransactionControl, count int) (Result, error) {
	return x.executeControl(control, count)
}

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

// ScopeSnapshotAnalysisPhase calls x.snapshotAnalysisPhase.
func ScopeSnapshotAnalysisPhase(x *StatementScope, stmt parser.Statement) error {
	return x.snapshotAnalysisPhase(stmt)
}

// ScopeSnapshotDataPhase calls x.snapshotDataPhase.
func ScopeSnapshotDataPhase(x *StatementScope) error {
	return x.snapshotDataPhase()
}

// ScopeSnapshotExecutionAdmission calls x.snapshotExecutionAdmission.
func ScopeSnapshotExecutionAdmission(x *StatementScope, stmt parser.Statement) error {
	return x.snapshotExecutionAdmission(stmt)
}

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

// OwnerExtensionCursorCleanup returns x.cursorCleanup.
func OwnerExtensionCursorCleanup(x *ownerExtension) []*sqlCursor { return x.cursorCleanup }

// OwnerExtensionForeignRef returns &x.foreign.
func OwnerExtensionForeignRef(x *ownerExtension) *fdw.Transaction { return &x.foreign }

// OwnerExtensionRetireForeign calls x.retireForeign.
func OwnerExtensionRetireForeign(x *ownerExtension) {
	x.retireForeign()
}

// PayloadNotifications returns x.notifications.
func PayloadNotifications(x *sessionPayload) session.NotificationListener { return x.notifications }

// PayloadNotificationsRef returns &x.notifications.
func PayloadNotificationsRef(x *sessionPayload) *session.NotificationListener {
	return &x.notifications
}

// PayloadSQLRoutines returns x.sqlRoutines.
func PayloadSQLRoutines(x *sessionPayload) map[ids.ObjectAddress]sqlRoutineAnalysis {
	return x.sqlRoutines
}

// TemporarySessionIndexesPrevious returns x.previous.
func TemporarySessionIndexesPrevious(x *temporarySessionIndexes) map[ids.ObjectAddress]*storage.IndexRoot {
	return x.previous
}

// TemporarySessionIndexesRoots returns x.roots.
func TemporarySessionIndexesRoots(x *temporarySessionIndexes) map[ids.ObjectAddress]*storage.IndexRoot {
	return x.roots
}

// FinishNotificationBoundary calls finishNotificationBoundary.
func FinishNotificationBoundary(t *session.TransactionOwner) {
	finishNotificationBoundary(t)
}

// NewStatementScope calls newStatementScope.
func NewStatementScope(op *session.Operation, owner *session.TransactionOwner, start time.Time) *StatementScope {
	return newStatementScope(op, owner, start)
}

// NotificationPublicationError calls notificationPublicationError.
func NotificationPublicationError(p *notificationPublication) error {
	return notificationPublicationError(p)
}

// OwnerHasForeign calls ownerHasForeign.
func OwnerHasForeign(t *session.TransactionOwner) bool {
	return ownerHasForeign(t)
}

// PublicationNeedsXID calls publicationNeedsXID.
func PublicationNeedsXID(writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, phase *session.PreparedPublication) bool {
	return publicationNeedsXID(writes, deletes, c, phase)
}

// EngineBeginLocked calls x.beginLocked.
func EngineBeginLocked(x *Engine, op *session.Operation, t *session.TransactionOwner, state session.TxState, start time.Time) error {
	return x.beginLocked(op, t, state, start)
}

// EngineInstallNotificationPublicationLocked calls x.installNotificationPublicationLocked.
func EngineInstallNotificationPublicationLocked(x *Engine, p *notificationPublication) {
	x.installNotificationPublicationLocked(p)
}

// EngineNotificationQueueUsageLocked calls x.notificationQueueUsageLocked.
func EngineNotificationQueueUsageLocked(x *Engine) float64 {
	return x.notificationQueueUsageLocked()
}

// EnginePinUniqueKey calls x.pinUniqueKey.
func EnginePinUniqueKey(x *Engine, ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering index.Ordering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	return x.pinUniqueKey(ctx, ticket, ref, ordering, grouping, values)
}

// EnginePollNotification calls x.pollNotification.
func EnginePollNotification(x *Engine, s *session.Session, check func() error) (session.Notification, bool, <-chan struct{}, error) {
	return x.pollNotification(s, check)
}

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

// OwnerExtensionExclusionModes returns x.exclusionModes.
func OwnerExtensionExclusionModes(x ownerExtension) index.ExclusionModes { return x.exclusionModes }

// OwnerExtensionForeign returns x.foreign.
func OwnerExtensionForeign(x ownerExtension) fdw.Transaction { return x.foreign }

// OwnerExtensionValueCursorCleanup returns x.cursorCleanup.
func OwnerExtensionValueCursorCleanup(x ownerExtension) []*sqlCursor { return x.cursorCleanup }

// SetOwnerExtensionCursorCleanup sets x.cursorCleanup.
func SetOwnerExtensionCursorCleanup(x *ownerExtension, v []*sqlCursor) { x.cursorCleanup = v }

// EngineValidateOperation calls x.validateOperation.
func EngineValidateOperation(x *Engine, op *session.Operation) error {
	return x.validateOperation(op)
}

// ScopeAcquireDataViewLocked calls x.acquireDataViewLocked.
func ScopeAcquireDataViewLocked(x *StatementScope, e *Engine, ref ids.ObjectRef) (*visibility.View, error) {
	return x.acquireDataViewLocked(e, ref)
}
