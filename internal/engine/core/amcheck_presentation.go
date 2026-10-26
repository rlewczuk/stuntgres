package core

// Nested commands recapture codecs and lookup-bearing inputs from the effective
// invocation image. Caller presentation caches and the session remain untouched.
func (s *StatementScope) refreshRestrictedPresentation() error {
	store := s.Op.EffectiveSettings()
	s.Input, s.Output = store.Presentation()
	store.Mu.Lock()
	s.RowSecurity = store.Value("row_security") == "on"
	store.Mu.Unlock()
	s.Inquiry = inquirySnapshot{}
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
