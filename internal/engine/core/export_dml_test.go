package core

import (
	"context"
	"io"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ExecuteMerge calls executeMerge.
func ExecuteMerge(ctx context.Context, scope *StatementScope, b analyze.Binding, target *exec.Source, inputs *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	return executeMerge(ctx, scope, b, target, inputs, parameters, schema)
}

// ExecuteUpdate calls executeUpdate.
func ExecuteUpdate(ctx context.Context, s *StatementScope, b analyze.Binding, source *exec.Source, from *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	return executeUpdate(ctx, s, b, source, from, parameters, schema)
}

// OutcomeResult calls outcomeResult.
func OutcomeResult(outcome dml.Outcome) Result {
	return outcomeResult(outcome)
}

// EngineExecuteCTECommandsLocked calls x.executeCTECommandsLocked.
func EngineExecuteCTECommandsLocked(x *Engine, scope *StatementScope, statement parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (Result, error) {
	return x.executeCTECommandsLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor)
}

// MutationTargetLockedIdentityOnly returns x.lockedIdentityOnly.
func MutationTargetLockedIdentityOnly(x *MutationTarget) bool { return x.lockedIdentityOnly }

// SetStatementMutationEffectAdmission sets x.effectAdmission.
func SetStatementMutationEffectAdmission(x *StatementMutation, v foreignEffectAdmission) {
	x.effectAdmission = v
}

// StatementMutationEffectAdmissionRef returns &x.effectAdmission.
func StatementMutationEffectAdmissionRef(x *StatementMutation) *foreignEffectAdmission {
	return &x.effectAdmission
}

// ScopeCompleteCommandEffects calls x.completeCommandEffects.
func ScopeCompleteCommandEffects(x *StatementScope, allExclusions bool, bounded bool) error {
	return x.completeCommandEffects(allExclusions, bounded)
}

// ScopeCopyCommandError calls x.copyCommandError.
func ScopeCopyCommandError(x *StatementScope, err error) error {
	return x.copyCommandError(err)
}

// ScopeCopyIO calls x.copyIO.
func ScopeCopyIO(x *StatementScope, call func() (int, error)) (int, error) {
	return x.copyIO(call)
}

// ScopeMergeInput calls x.mergeInput.
func ScopeMergeInput(x *StatementScope, ctx context.Context, b analyze.Binding, target *exec.Source, inputs *exec.Source, parameters []sqltype.Value, schema string) dml.MergeInput {
	return x.mergeInput(ctx, b, target, inputs, parameters, schema)
}

// ScopeReleaseCopyReservation calls x.releaseCopyReservation.
func ScopeReleaseCopyReservation(x *StatementScope) {
	x.releaseCopyReservation()
}

// ScopeReserveCopy calls x.reserveCopy.
func ScopeReserveCopy(x *StatementScope) error {
	return x.reserveCopy()
}

// ScopeTruncateOrZero calls x.truncateOrZero.
func ScopeTruncateOrZero(x *StatementScope) dml.TruncateScope {
	return x.truncateOrZero()
}

// ScopeTruncateSlot returns the command-local manifest without allocating it.
func ScopeTruncateSlot(x *StatementScope) *dml.TruncateScope { return x.truncateSlot }

// ScopeStageTruncate executes the admitted manifest's actual storage guards.
func ScopeStageTruncate(x *StatementScope) error { return x.stageTruncate() }

// EngineRowDiagnosticDetail calls x.rowDiagnosticDetail.
func EngineRowDiagnosticDetail(x *Engine, s *StatementScope, b *analyze.Binding, row []sqltype.Value, suppressEmpty bool) (string, error) {
	return x.rowDiagnosticDetail(s, b, row, suppressEmpty)
}

// ScopeAcquireInheritedMutationTargets calls x.acquireInheritedMutationTargets.
func ScopeAcquireInheritedMutationTargets(x *StatementScope, statement parser.Statement) error {
	return x.acquireInheritedMutationTargets(statement)
}

// ScopeMutationStorage calls x.mutationStorage.
func ScopeMutationStorage(x *StatementScope, ref ids.ObjectRef) *storage.Relation {
	return x.mutationStorage(ref)
}

// ScopeCopyProtocol calls x.copyProtocol.
func ScopeCopyProtocol(x *StatementScope, syntax *parser.Copy, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (int64, error) {
	return x.copyProtocol(syntax, source, destination, hooks)
}
