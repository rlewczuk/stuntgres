package engine

// Only actual blocking-grant continuations call this, never a row Eval or an
// uncontended key probe. TxRef/CommandID are phase coordinates, not a substitute
// for this fresh coherent authority capture. All checks after unlock are pure
// graph admission: no provider, CHECK truth, codec or output replay.
func (s *StatementScope) readmitProgramsAfterWait() error {
	if len(s.Domains.PreparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.IndexBindings) == 0 {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	err := s.ValidateLockedRelation(e)
	var auth AuthorizationView
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
		if err == nil {
			auth.Recapture(e.PrivateCatalogLocked(s.Owner.Catalog))
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	return s.readmitPreparedPrograms(auth)
}
