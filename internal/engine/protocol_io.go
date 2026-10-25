package engine

import "errors"

// ProtocolOutput releases retained simple-message admission for serialized
// output. A logical reservation prevents owner mutation until the callback ends.
// It is valid after Finish but before NextStatement/FinishMessage.
func (s *StatementScope) ProtocolOutput(write func() error) (err error) {
	if s == nil || s.self != s || !s.Finished || !s.message || s.messageEnded || !s.ownsMessageScope() || s.Owner.CopyLease != nil {
		return errors.New("stuntgres: inactive protocol output admission")
	}
	err = s.protocolTerminalIO(write)
	if err == nil {
		err = s.Engine().ValidateOperation(s.Op)
	}
	return err
}

// Terminal cleanup must run even after cancellation. This reserves only owner
// lifetime: it does not resume an executor or authorize publication afterward.
func (s *StatementScope) protocolTerminalIO(call func() error) error {
	if s == nil || s.self != s || s.Owner.CopyLease != nil && s.Owner.CopyLease != &s.lease {
		return errors.New("stuntgres: invalid protocol terminal reservation")
	}
	t := s.Owner
	borrowed := t.CopyLease == &s.lease
	if !borrowed {
		t.CopyLease, t.CopyDone = &s.lease, make(chan struct{})
	}
	e := s.Engine()
	fenced := s.ReplacementRead
	if fenced {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	t.Admission.Unlock()
	defer func() {
		t.Admission.Lock()
		if fenced {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		if !borrowed {
			s.releaseCopyReservation()
		}
	}()
	return call()
}
