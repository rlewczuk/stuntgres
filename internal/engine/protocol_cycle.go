package engine

// ProtocolCycle retains a transaction between frontend commands, never an
// operation, timeout context or admission lock. The physical session owns it.
type ProtocolCycle struct {
	Owner    *transactionOwner
	Active   bool
	Executed bool
}

func (s *Session) NewProtocolCycle() *ProtocolCycle {
	t := s.Transactions
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.ProtocolCycle == nil {
		t.ProtocolCycle = &ProtocolCycle{Owner: t}
	}
	return t.ProtocolCycle
}

func (t *transactionOwner) RetainsTransaction() bool {
	return t.RetainedMessage || t.ProtocolCycle != nil && t.ProtocolCycle.Active
}
