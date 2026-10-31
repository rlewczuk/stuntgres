package core

import (
	"sync"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// StatementHost names statementHost.
type StatementHost = statementHost

// EngineExecuteStatement calls x.executeStatement.
func EngineExecuteStatement(x *Engine, scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	return x.executeStatement(scope, stmt, count, importFrame)
}

// ScopeAcquireRelationTarget calls x.acquireRelationTarget.
func ScopeAcquireRelationTarget(x *StatementScope, stmt parser.Statement, authorityStmt parser.Statement, execute bool, source bool) error {
	return x.acquireRelationTarget(stmt, authorityStmt, execute, source)
}

// ScopeExecuteDiscard calls x.executeDiscard.
func ScopeExecuteDiscard(x *StatementScope, raw *parser.Discard, count int) (Result, error) {
	return x.executeDiscard(raw, count)
}

// ScopeStageDiscardTemporary calls x.stageDiscardTemporary.
func ScopeStageDiscardTemporary(x *StatementScope, closeCursors bool) error {
	return x.stageDiscardTemporary(closeCursors)
}

// ReplacementAdmissionMuRef returns &x.mu.
func ReplacementAdmissionMuRef(x *replacementAdmission) *sync.Mutex { return &x.mu }

// ReplacementAdmissionReaders returns x.readers.
func ReplacementAdmissionReaders(x *replacementAdmission) int { return x.readers }

// ReplacementAdmissionWriters returns x.writers.
func ReplacementAdmissionWriters(x *replacementAdmission) int { return x.writers }

// ReplacementAdmissionWriting returns x.writing.
func ReplacementAdmissionWriting(x *replacementAdmission) bool { return x.writing }

// EngineRegisterExtension calls x.registerExtension.
func EngineRegisterExtension(x *Engine, name string, implementation extension.Installer) error {
	return x.registerExtension(name, implementation)
}

// ScopeForeignWrites calls x.foreignWrites.
func ScopeForeignWrites(x *StatementScope) fdw.WriteRuntime {
	return x.foreignWrites()
}

// ScopePresentationOrZero calls x.presentationOrZero.
func ScopePresentationOrZero(x *StatementScope) presentationCaches {
	return x.presentationOrZero()
}

// PreparedCommandExecutionFields holds the preparedCommandExecution fields tests set.
type PreparedCommandExecutionFields struct {
	PolicyWaits uint64
}

// NewPreparedCommandExecution builds a preparedCommandExecution from f.
func NewPreparedCommandExecution(f PreparedCommandExecutionFields) *preparedCommandExecution {
	return &preparedCommandExecution{policyWaits: f.PolicyWaits}
}

// PreparedCommandExecutionPolicyWaits returns x.policyWaits.
func PreparedCommandExecutionPolicyWaits(x *preparedCommandExecution) uint64 { return x.policyWaits }

// PresentationCachesConversionDefaults returns x.conversionDefaults.
func PresentationCachesConversionDefaults(x presentationCaches) map[[2]int]conversionDefault {
	return x.conversionDefaults
}

// StatementHostFields holds the statementHost fields tests set.
type StatementHostFields struct {
	Op *session.Operation
}

// NewStatementHost builds a statementHost from f.
func NewStatementHost(f StatementHostFields) *statementHost {
	return &statementHost{Op: f.Op}
}

// AuthorizationOf calls authorizationOf.
func AuthorizationOf(a analyze.Authority) *AuthorizationView { return authorizationOf(a) }

// BindingHostView calls x.view.
func BindingHostView(x *BindingHost, a analyze.Authority) AuthorizationView {
	return x.view(a)
}
