package engine

// ProtocolCycle retains a transaction between frontend commands, never an
// operation, timeout context or admission lock. The physical session owns it.
type ProtocolCycle struct {
	owner    *transactionOwner
	active   bool
	executed bool
}

func (s *Session) NewProtocolCycle() *ProtocolCycle {
	t := s.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.protocolCycle == nil {
		t.protocolCycle = &ProtocolCycle{owner: t}
	}
	return t.protocolCycle
}

func (t *transactionOwner) retainsTransaction() bool {
	return t.retainedMessage || t.protocolCycle != nil && t.protocolCycle.active
}
