package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// initializeIndexMethodLocked produces the native operation authority while the
// caller holds Mu. The factory is a finite pure native selection, not a user
// kernel: neither unlocking nor a lock-reentering expression host is involved.
func (s *StatementScope) initializeIndexMethodLocked(read catalog.Read, method *catalog.AccessMethod) (catalog.IndexMethod, error) {
	if method == nil || read.Get(method.Address()) != method {
		return 0, ids.ErrCatalogReplaced
	}
	if err := (catalog.AccessMethodProvider{}).Validate(read, method); err != nil {
		return 0, err
	}
	if method.MethodKind() != "i" {
		return 0, fmt.Errorf("stuntgres: invalid index access method factory")
	}
	if s == nil || s.Engine() == nil {
		return 0, fmt.Errorf("stuntgres: missing access method factory owner")
	}
	e := s.Engine()
	if e.Sepgsql != nil {
		if s.Op == nil || s.Op.Session == nil || ActiveScopeOfLease(s.Op.Lease) != s {
			return 0, fmt.Errorf("stuntgres: missing access method factory operation")
		}
		if err := s.Op.Context().Err(); err != nil {
			return 0, err
		}
		mark := s.MarkSepgsqlObservations()
		auth, err := s.RoutineAuthorizationLocked()
		handler, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		if err == nil && (!ok || read.Owner() != auth.Catalog.Owner() || read.Domain() != auth.Catalog.Domain() || read.Epoch() != auth.Catalog.Epoch() || auth.Catalog.Get(method.Address()) != method || auth.Catalog.Get(handler.Address()) != handler) {
			err = ids.ErrCatalogReplaced
		}
		target, transition := "", false
		if err == nil {
			auth.Recapture(read)
			target, transition, err = s.sepgsqlBodyDecision(auth, handler, false, nil)
		}
		if err != nil {
			s.RestoreSepgsqlObservations(mark)
			return 0, err
		}
		if transition {
			token := s.Op.Session.Sepgsql.Enter(target)
			defer token.Leave()
		}
	}
	implementation, ok := method.IndexImplementation()
	if !ok {
		return 0, fmt.Errorf("stuntgres: unavailable index operation authority")
	}
	return implementation, nil
}

func (h DDLHost) InitializeIndexMethod(auth ddl.Authority, method *catalog.AccessMethod) (catalog.IndexMethod, error) {
	read := h.AccessView(auth).Catalog
	if err := (catalog.AccessMethodProvider{}).Validate(read, method); err != nil {
		return 0, err
	}
	// The isolated import is already a genuine internal DDL owner, not a SQL
	// client borrowing a missing scope or an initial simulated subject.
	if h.E.Sepgsql == nil || h.E.ImportTargets != nil {
		implementation, ok := method.IndexImplementation()
		if !ok {
			return 0, fmt.Errorf("stuntgres: unavailable index operation authority")
		}
		return implementation, nil
	}
	if h.Op == nil {
		return 0, fmt.Errorf("stuntgres: missing access method DDL operation")
	}
	scope := ActiveScopeOfLease(h.Op.Lease)
	if scope == nil || scope.Op != h.Op || scope.Engine() != h.E {
		return 0, fmt.Errorf("stuntgres: foreign access method DDL operation")
	}
	implementation, err := scope.initializeIndexMethodLocked(read, method)
	if err != nil {
		return 0, err
	}
	if h.CatalogStage != nil {
		h.AddDomainBase(method)
		handler, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		h.AddDomainBase(handler)
	}
	return implementation, nil
}

// InitializeInquiryIndexMethod owns the actual amutils handler factory, not
// catalog projection or metadata validation. Inquiry callers hold no locks.
func (h *statementHost) InitializeInquiryIndexMethod(read catalog.Read, method *catalog.AccessMethod) (catalog.IndexMethod, error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return s.initializeIndexMethodLocked(read, method)
}

func (s *StatementScope) InitializeCapturedIndexMethodLocked(read catalog.Read, method *catalog.AccessMethod) (catalog.IndexMethod, error) {
	return s.initializeIndexMethodLocked(read, method)
}

func (h *statementHost) InitializeCapturedIndexMethodLocked(read catalog.Read, method *catalog.AccessMethod) (catalog.IndexMethod, error) {
	return h.scope().initializeIndexMethodLocked(read, method)
}

// ValidateAccessMethodFactoriesLocked fences the already completed factory
// phases before maintenance's first real install. It never reallows them.
func (s *StatementScope) ValidateAccessMethodFactoriesLocked() error {
	if err := s.Active(); err != nil {
		return err
	}
	return s.validateSepgsqlObservations(s.Engine().Catalog.State)
}
