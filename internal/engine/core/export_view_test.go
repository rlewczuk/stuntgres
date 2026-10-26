package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/view"
)

// RowSecurityProgram names rowSecurityProgram.
type RowSecurityProgram = rowSecurityProgram

// EngineCaptureRowSecurityLocked calls x.captureRowSecurityLocked.
func EngineCaptureRowSecurityLocked(x *Engine, s *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool, selectRead bool) (*exec.RLSProgram, error) {
	return x.captureRowSecurityLocked(s, d, principal, command, proposed, selectRead)
}

// EngineEndRowSecurityAdmissionLocked calls x.endRowSecurityAdmissionLocked.
func EngineEndRowSecurityAdmissionLocked(x *Engine, s *StatementScope, err error) error {
	return x.endRowSecurityAdmissionLocked(s, err)
}

// ScopeAcquireBoundViewRelationModes calls x.acquireBoundViewRelationModes.
func ScopeAcquireBoundViewRelationModes(x *StatementScope, refs []ids.ObjectRef, modes map[ids.ObjectRef]lock.Mode) error {
	return x.acquireBoundViewRelationModes(refs, modes)
}

// ScopeAcquireViewWriteTarget calls x.acquireViewWriteTarget.
func ScopeAcquireViewWriteTarget(x *StatementScope, w *view.Write) error {
	return x.acquireViewWriteTarget(w)
}

// ScopeTakeRowSecurityPrograms calls x.takeRowSecurityPrograms.
func ScopeTakeRowSecurityPrograms(x *StatementScope, c *sqlCursor) {
	x.takeRowSecurityPrograms(c)
}

// RowSecurityProgramFields holds the rowSecurityProgram fields tests set.
type RowSecurityProgramFields struct {
	RLSProgram exec.RLSProgram
}

// NewRowSecurityProgram builds a rowSecurityProgram from f.
func NewRowSecurityProgram(f RowSecurityProgramFields) *rowSecurityProgram {
	return &rowSecurityProgram{RLSProgram: f.RLSProgram}
}

// RowSecurityProgramSources returns x.sources.
func RowSecurityProgramSources(x *rowSecurityProgram) []policySourcePreparation { return x.sources }

// RuleAuthorizationOf calls ruleAuthorizationOf.
func RuleAuthorizationOf(auth view.RuleAuthority) AuthorizationView {
	return ruleAuthorizationOf(auth)
}

// RuleCommandScratch names ruleCommandScratch.
type RuleCommandScratch = ruleCommandScratch

// ScopeInstallRuleScratch calls x.installRuleScratch.
func ScopeInstallRuleScratch(x *StatementScope, c RuleCommandScratch) {
	x.installRuleScratch(c)
}

// ScopeRuleScratch calls x.ruleScratch.
func ScopeRuleScratch(x *StatementScope) RuleCommandScratch {
	return x.ruleScratch()
}
