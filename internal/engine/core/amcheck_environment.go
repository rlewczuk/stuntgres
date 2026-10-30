package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

// WithAmcheckOwner keeps the invocation environment outside the lexical scratch
// SQL/PL activation replaces. No session identity/settings are changed.
func (s *StatementScope) WithAmcheckOwner(parent *catalog.Relation, work func(*exec.Context) error) error {
	op, e := s.Op, s.Engine()
	previous := op.Restricted
	identity, inquiry, input, output, RoutineAuth := s.Identity, s.Inquiry, s.Input, s.Output, s.RoutineAuth
	RowSecurity, presentation := s.RowSecurity, s.presentationSlot
	threshold, nonstandard, lockTimeout := op.NoticeThreshold, op.NonstandardStrings, op.LockTimeout
	defer func() {
		current := op.EffectiveSettings()
		current.Mu.Lock()
		beforeRole := current.TypedValue("session_replication_role").Text
		current.Mu.Unlock()
		op.Restricted = previous
		restored := op.EffectiveSettings()
		restored.Mu.Lock()
		restored.RuleRoleChanged(beforeRole)
		restored.Mu.Unlock()
		s.Identity, s.Inquiry, s.Input, s.Output, s.RoutineAuth = identity, inquiry, input, output, RoutineAuth
		s.RowSecurity, s.presentationSlot = RowSecurity, presentation
		op.NoticeThreshold, op.NonstandardStrings, op.LockTimeout = threshold, nonstandard, lockTimeout
	}()
	// Preserve the active query's immutable read frontier before restricted
	// presentation replaces Inquiry and callable admission selects the owner.
	// Utility/CHECK invocations use the same existing private-catalog capture.
	e.Mu.Lock()
	_, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	op.Restricted = &session.RestrictedInvocation{Owner: parent.Owner, Parent: previous, SafePath: true}
	s.presentationSlot = nil
	// Force lazy settings capture before taking metadata/identity mutexes.
	settings := op.EffectiveSettings()
	e.Mu.Lock()
	definition := e.Settings.Definitions()["search_path"]
	e.Mu.Unlock()
	value, _, err := settings.ParseValue(definition, "pg_catalog, pg_temp")
	if err != nil {
		return err
	}
	settings.Values["search_path"], settings.Typed["search_path"] = definition.Format(value), value
	settings.Mu.Lock()
	op.RefreshInvocationSettingsLocked(settings)
	settings.Mu.Unlock()
	if err := s.refreshRestrictedPresentation(); err != nil {
		return err
	}
	auth := s.RoutineAuth
	CaptureEnumOperationSafety(op, &s.Input)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.ActivateRuntime(s.executionContext()), Host: HostOf(s), Catalog: auth.Catalog, Input: s.Input, Output: s.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	evaluation.Input.DomainChecks = &evaluation
	return work(&evaluation)
}
