package engine

import "errors"

func (e *Engine) StartProtocolCommand(op *Operation, cycle *ProtocolCycle) (*StatementScope, error) {
	if op == nil || cycle == nil || cycle.owner != op.Session.Transactions || engineOf(op.Session) != e {
		return nil, errors.New("stuntgres: foreign protocol cycle")
	}
	s, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	s.owner.mu.Lock()
	cycle.active = true
	err = s.admitTransactionLocked(e)
	s.owner.mu.Unlock()
	if err != nil {
		return nil, s.Finish(err)
	}
	s.protocol().command = true
	return s, nil
}

// ProtocolSimpleQuery applies the released simple-query boundary to a pending
// extended segment. Executed pipelines survive single/empty Q; a multi-Q block
// has its own explicit implicit-block end. Parse/Bind-only work ends with Q.
func (s *StatementScope) ProtocolSimpleQuery(statements int) {
	s.protocol().simple = statements != 0
	s.owner.mu.Lock()
	if c := s.owner.protocolCycle; c != nil {
		c.active = c.executed && statements <= 1
		if !c.active {
			c.executed = false
		}
	}
	s.owner.mu.Unlock()
}

func (e *Engine) SyncProtocolCycle(op *Operation, cycle *ProtocolCycle) error {
	if op == nil || cycle == nil || cycle.owner != op.Session.Transactions || engineOf(op.Session) != e {
		return errors.New("stuntgres: foreign protocol cycle")
	}
	s, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	t := s.owner
	t.mu.Lock()
	state, ref := t.state, t.ref
	cycle.active, cycle.executed = false, false
	t.mu.Unlock()
	if state == TxImplicit {
		_, err = e.endAdmitted(op, t, ref, true, false, true, s.statementStart, s)
	}
	return s.Finish(err)
}
