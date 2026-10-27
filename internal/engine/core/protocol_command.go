package core

import (
	"errors"
	"stuntgres/internal/engine/session"
)

func (e *Engine) StartProtocolCommand(op *session.Operation, cycle *session.ProtocolCycle) (*StatementScope, error) {
	if op == nil || cycle == nil || cycle.Owner != op.Session.Transactions || EngineOf(op.Session) != e {
		return nil, errors.New("stuntgres: foreign protocol cycle")
	}
	s, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	s.Owner.Mu.Lock()
	cycle.Active = true
	err = s.admitTransactionLocked(e)
	s.Owner.Mu.Unlock()
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
	s.Owner.Mu.Lock()
	if c := s.Owner.ProtocolCycle; c != nil {
		c.Active = c.Executed && statements <= 1
		if !c.Active {
			c.Executed = false
		}
	}
	s.Owner.Mu.Unlock()
}

func (e *Engine) SyncProtocolCycle(op *session.Operation, cycle *session.ProtocolCycle) error {
	if op == nil || cycle == nil || cycle.Owner != op.Session.Transactions || EngineOf(op.Session) != e {
		return errors.New("stuntgres: foreign protocol cycle")
	}
	s, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	t := s.Owner
	t.Mu.Lock()
	state, ref := t.State, t.Ref
	cycle.Active, cycle.Executed = false, false
	t.Mu.Unlock()
	if state == session.TxImplicit {
		_, err = e.endAdmitted(op, t, ref, true, restartNone, true, s.statementStart, s)
	}
	return s.Finish(err)
}
