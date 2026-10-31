package core

import (
	"context"
	"time"

	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
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

// EngineEnrollSerializableLocked calls x.enrollSerializableLocked.
func EngineEnrollSerializableLocked(x *Engine, ref ids.TxRef, isolation session.Isolation) {
	x.enrollSerializableLocked(ref, isolation)
}

// EngineFenceSerializableLocked calls x.fenceSerializableLocked.
func EngineFenceSerializableLocked(x *Engine) {
	x.fenceSerializableLocked()
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

// EngineTransactionAgeLocked calls x.transactionAgeLocked.
func EngineTransactionAgeLocked(x *Engine, ref ids.TxRef, xid uint32) (int32, error) {
	return x.transactionAgeLocked(ref, xid)
}

// ScopeAttachTransactionLocked calls x.attachTransactionLocked.
func ScopeAttachTransactionLocked(x *StatementScope) {
	x.attachTransactionLocked()
}

// ScopeExecuteControl calls x.executeControl.
func ScopeExecuteControl(x *StatementScope, control *parser.TransactionControl, count int) (Result, error) {
	return x.executeControl(control, count)
}

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

// OwnerExtensionCursorCleanup returns x.cursorCleanup.
func OwnerExtensionCursorCleanup(x *ownerExtension) []*sqlCursor { return x.cursorCleanup }

// OwnerExtensionForeignRef returns &x.foreign.
func OwnerExtensionForeignRef(x *ownerExtension) *fdw.Transaction { return &x.foreign }

// OwnerExtensionRetireForeign calls x.retireForeign.
func OwnerExtensionRetireForeign(x *ownerExtension) {
	x.retireForeign()
}

// NewStatementScope calls newStatementScope.
func NewStatementScope(op *session.Operation, owner *session.TransactionOwner, start time.Time) *StatementScope {
	return newStatementScope(op, owner, start)
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

// EnginePinUniqueKey calls x.pinUniqueKey.
func EnginePinUniqueKey(x *Engine, ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering index.Ordering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	return x.pinUniqueKey(ctx, ticket, ref, ordering, grouping, values)
}

// OwnerExtensionConstraintModes returns x.constraintModes.
func OwnerExtensionConstraintModes(x ownerExtension) index.ConstraintModes { return x.constraintModes }

// OwnerExtensionForeign returns x.foreign.
func OwnerExtensionForeign(x ownerExtension) fdw.Transaction { return x.foreign }

// OwnerExtensionValueCursorCleanup returns x.cursorCleanup.
func OwnerExtensionValueCursorCleanup(x ownerExtension) []*sqlCursor { return x.cursorCleanup }

// SetOwnerExtensionCursorCleanup sets x.cursorCleanup.
func SetOwnerExtensionCursorCleanup(x *ownerExtension, v []*sqlCursor) { x.cursorCleanup = v }

// ScopeAcquireDataViewLocked calls x.acquireDataViewLocked.
func ScopeAcquireDataViewLocked(x *StatementScope, e *Engine, ref ids.ObjectRef) (*visibility.View, error) {
	return x.acquireDataViewLocked(e, ref)
}
