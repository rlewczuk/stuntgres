package engine

import "errors"

// ProtocolOutput releases retained simple-message admission for serialized
// output. A logical reservation prevents owner mutation until the callback ends.
// It is valid after Finish but before NextStatement/FinishMessage.
func (s *StatementScope) ProtocolOutput(write func() error) (err error) {
	if s == nil || s.self != s || !s.finished || !s.message || s.messageEnded || !s.ownsMessageScope() || s.owner.copyLease != nil {
		return errors.New("stuntgres: inactive protocol output admission")
	}
	err = s.protocolTerminalIO(write)
	if err == nil {
		err = s.engine().validateOperation(s.op)
	}
	return err
}

// Terminal cleanup must run even after cancellation. This reserves only owner
// lifetime: it does not resume an executor or authorize publication afterward.
func (s *StatementScope) protocolTerminalIO(call func() error) error {
	if s == nil || s.self != s || s.owner.copyLease != nil && s.owner.copyLease != &s.lease {
		return errors.New("stuntgres: invalid protocol terminal reservation")
	}
	t := s.owner
	borrowed := t.copyLease == &s.lease
	if !borrowed {
		t.copyLease, t.copyDone = &s.lease, make(chan struct{})
	}
	e := s.engine()
	fenced := s.replacementRead
	if fenced {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	t.admission.Unlock()
	defer func() {
		t.admission.Lock()
		if fenced {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		if !borrowed {
			s.releaseCopyReservation()
		}
	}()
	return call()
}
