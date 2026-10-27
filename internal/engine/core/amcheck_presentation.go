package core

import "stuntgres/internal/sqltype"

// Nested commands recapture codecs, effective identity, inquiry and lookup inputs.
// Used by both restricted maintenance and ordinary routine-local environments.
func (s *StatementScope) refreshRestrictedPresentation() error {
	store := s.Op.EffectiveSettings()
	input, output := store.Presentation()
	// Settings recapture must not erase the enclosing execution boundary or
	// demanded output formatter ownership. Catalog input resolvers are rebuilt.
	input.TransactionStart = s.Input.TransactionStart
	input.LockedDomainInput, input.DomainChecks = s.Input.LockedDomainInput, s.Input.DomainChecks
	input.ExactSnapshot, input.RawBoxReplay = s.Input.ExactSnapshot, s.Input.RawBoxReplay
	input.EnumMemberSafe, input.FTS = s.Input.EnumMemberSafe, s.Input.FTS
	output.Regclass, output.Regtype, output.Regproc = s.Output.Regclass, s.Output.Regtype, s.Output.Regproc
	output.Regrole, output.Operator = s.Output.Regrole, s.Output.Operator
	output.Regcollation, output.Regnamespace, output.RegTS = s.Output.Regcollation, s.Output.Regnamespace, s.Output.RegTS
	output.ACLItem = s.Output.ACLItem
	s.Input, s.Output = input, output
	store.Mu.Lock()
	s.RowSecurity = store.Value("row_security") == "on"
	store.Mu.Unlock()
	// Catalog, visibility and output spellings belong to the enclosing query.
	// Only lexical namespace results depend on this actor/settings transition.
	s.Inquiry.CurrentSchemaResult = sqltype.Value{}
	s.Inquiry.searchPathResults = [2]sqltype.Value{}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		err = s.CaptureIdentityLocked(auth)
	}
	if err == nil {
		err = e.CaptureElementInput(auth, s.Op, &s.Input)
	}
	if err == nil {
		s.Input.ACLItem = e.CaptureACLItemInput(auth, s.Op)
	}
	if err == nil {
		s.Input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
	}
	if err == nil {
		s.RoutineAuth = auth
	}
	return err
}
