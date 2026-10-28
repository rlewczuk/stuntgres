package core

import (
	"errors"
	"io"

	"stuntgres/internal/engine/ids"
)

func (s *StatementScope) reserveCopy() error {
	if err := s.Active(); err != nil {
		return err
	}
	if s.Owner.CopyLease != nil {
		return errors.New("stuntgres: COPY owner is already reserved")
	}
	s.Owner.CopyLease = &s.lease
	s.Owner.CopyDone = make(chan struct{})
	return nil
}
func (s *StatementScope) releaseCopyReservation() {
	if s.self == s && s.Owner.CopyLease == &s.lease {
		done := s.Owner.CopyDone
		s.Owner.CopyLease = nil
		s.Owner.CopyDone = nil
		close(done)
	}
}
func (s *StatementScope) copyIO(call func() (int, error)) (n int, err error) {
	if s == nil || s.self != s || s.Finished || s.Owner.CopyLease != &s.lease {
		return 0, errors.New("stuntgres: invalid COPY reservation")
	}
	if err = s.Active(); err != nil {
		return 0, err
	}
	e := s.Engine()
	fenced := s.ReplacementRead
	if fenced {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	s.Owner.Admission.Unlock()
	defer func() {
		s.Owner.Admission.Lock()
		if fenced {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		resume := s.Active()
		if resume == nil && s.Owner.CopyLease != &s.lease {
			resume = errors.New("stuntgres: lost COPY reservation")
		}
		s.Owner.Mu.Lock()
		if resume == nil && (s.Owner.Ref != s.TxRef || s.Owner.Command != s.CommandID) {
			resume = errors.New("stuntgres: stale COPY owner")
		}
		if resume == nil {
			e.Mu.Lock()
			if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
				resume = ids.ErrCatalogReplaced
			} else {
				resume = s.ValidateLockedRelation(e)
			}
			e.Mu.Unlock()
		}
		s.Owner.Mu.Unlock()
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
	return r.scope.copyIO(func() (int, error) {
		n, err := r.source.Read(p)
		if n > 0 {
			r.scope.copyProgress.bytes.Add(int64(n))
		}
		return n, err
	})
}

type copyWriter struct {
	scope       *StatementScope
	destination io.Writer
}

func (w copyWriter) Write(p []byte) (int, error) {
	return w.scope.copyIO(func() (int, error) {
		n, err := w.destination.Write(p)
		if n > 0 {
			w.scope.copyProgress.bytes.Add(int64(n))
		}
		return n, err
	})
}
