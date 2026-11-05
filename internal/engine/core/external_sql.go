package core

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
)

// Called only by execution's foreign gateway after planning and lock release.
// The captured procedure token, never current spelling or numeric lookup, binds
// the capability. Registry iteration reads immutable installation state.
func (s *StatementScope) externalSQLCapability(handler *catalog.ForeignProcedure, request *catalog.ForeignRequest) error {
	request.ExternalSQL = nil
	if handler == nil || handler.ExternalProvider == nil {
		return nil
	}
	genuine := false
	for token := range s.Engine().Foreign.ProcedureTokens() {
		if token == handler {
			genuine = true
			break
		}
	}
	if !genuine {
		return errors.New("stuntgres: foreign external SQL installation token")
	}
	if err := session.ValidateOperation(s.Op.Session.Token().Domain(), s.Op); err != nil {
		return err
	}
	owner, err := s.externalSQLOwner()
	if err != nil {
		return err
	}
	request.ExternalSQL = owner.Capability(s.Op, handler.ExternalProvider, *request)
	return nil
}

// One lazily attached physical owner serves both authentic gateways. Session.Mu
// protects the payload pointer against native replacement's detached capture.
func (s *StatementScope) externalSQLOwner() (*remote.Owner, error) {
	physical := s.Op.Session
	payload := PayloadOf(physical)
	physical.Mu.Lock()
	owner := payload.externalSQL
	physical.Mu.Unlock()
	if owner != nil {
		return owner, nil
	}
	owner = remote.NewOwner(physical)
	if _, err := s.Op.Attach(session.ExternalSQL, owner); err != nil {
		return nil, err
	}
	physical.Mu.Lock()
	payload.externalSQL = owner
	physical.Mu.Unlock()
	return owner, nil
}
