package engine

import (
	"context"
	"errors"
	"io"

	"stuntgres/internal/engine/ids"
)

// Admission remains held for ordinary statements. Only a reserved COPY drops
// the mutex across external I/O; every owner mutator waits on its logical lease.
func (t *transactionOwner) acquireAdmission(ctx context.Context, op *Operation) error {
	waited := false
	for {
		t.admission.Lock()
		if t.copyLease == nil {
			if waited && op != nil {
				if err := engineOf(t.session).validateOperation(op); err != nil {
					t.admission.Unlock()
					return err
				}
			}
			return nil
		}
		done := t.copyDone
		t.admission.Unlock()
		select {
		case <-done:
			waited = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (s *StatementScope) reserveCopy() error {
	if err := s.active(); err != nil {
		return err
	}
	if s.owner.copyLease != nil {
		return errors.New("stuntgres: COPY owner is already reserved")
	}
	s.owner.copyLease = &s.lease
	s.owner.copyDone = make(chan struct{})
	return nil
}
func (s *StatementScope) releaseCopyReservation() {
	if s.self == s && s.owner.copyLease == &s.lease {
		done := s.owner.copyDone
		s.owner.copyLease = nil
		s.owner.copyDone = nil
		close(done)
	}
}
func (s *StatementScope) copyIO(call func() (int, error)) (n int, err error) {
	if s == nil || s.self != s || s.finished || s.owner.copyLease != &s.lease {
		return 0, errors.New("stuntgres: invalid COPY reservation")
	}
	if err = s.active(); err != nil {
		return 0, err
	}
	e := s.engine()
	fenced := s.replacementRead
	if fenced {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	s.owner.admission.Unlock()
	defer func() {
		s.owner.admission.Lock()
		if fenced {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		resume := s.active()
		if resume == nil && s.owner.copyLease != &s.lease {
			resume = errors.New("stuntgres: lost COPY reservation")
		}
		s.owner.mu.Lock()
		if resume == nil && (s.owner.ref != s.txRef || s.owner.command != s.command) {
			resume = errors.New("stuntgres: stale COPY owner")
		}
		if resume == nil {
			e.mu.Lock()
			if s.txRef.Epoch() != e.catalog.state.Epoch() {
				resume = ids.ErrCatalogReplaced
			} else {
				resume = s.validateLockedRelation(e)
			}
			e.mu.Unlock()
		}
		s.owner.mu.Unlock()
		if err == nil || err == io.EOF {
			if resume != nil {
				n = 0
				err = resume
			}
		}
	}()
	return call()
}

type copyReader struct {
	scope  *StatementScope
	source io.Reader
}

func (r copyReader) Read(p []byte) (int, error) {
	return r.scope.copyIO(func() (int, error) { return r.source.Read(p) })
}

type copyWriter struct {
	scope       *StatementScope
	destination io.Writer
}

func (w copyWriter) Write(p []byte) (int, error) {
	return w.scope.copyIO(func() (int, error) { return w.destination.Write(p) })
}
