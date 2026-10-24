package engine

import (
	"errors"
	"io"

	"stuntgres/internal/engine/ids"
)

func (s *StatementScope) reserveCopy() error {
	if err := s.active(); err != nil {
		return err
	}
	if s.owner.CopyLease != nil {
		return errors.New("stuntgres: COPY owner is already reserved")
	}
	s.owner.CopyLease = &s.lease
	s.owner.CopyDone = make(chan struct{})
	return nil
}
func (s *StatementScope) releaseCopyReservation() {
	if s.self == s && s.owner.CopyLease == &s.lease {
		done := s.owner.CopyDone
		s.owner.CopyLease = nil
		s.owner.CopyDone = nil
		close(done)
	}
}
func (s *StatementScope) copyIO(call func() (int, error)) (n int, err error) {
	if s == nil || s.self != s || s.finished || s.owner.CopyLease != &s.lease {
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
	s.owner.Admission.Unlock()
	defer func() {
		s.owner.Admission.Lock()
		if fenced {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		resume := s.active()
		if resume == nil && s.owner.CopyLease != &s.lease {
			resume = errors.New("stuntgres: lost COPY reservation")
		}
		s.owner.Mu.Lock()
		if resume == nil && (s.owner.Ref != s.txRef || s.owner.Command != s.command) {
			resume = errors.New("stuntgres: stale COPY owner")
		}
		if resume == nil {
			e.mu.Lock()
			if s.txRef.Epoch() != e.catalog.State.Epoch() {
				resume = ids.ErrCatalogReplaced
			} else {
				resume = s.validateLockedRelation(e)
			}
			e.mu.Unlock()
		}
		s.owner.Mu.Unlock()
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
