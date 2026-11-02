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
	payload := PayloadOf(s.Op.Session)
	if payload.externalSQL == nil {
		owner := remote.NewOwner(s.Op.Session)
		if _, err := s.Op.Attach(session.ExternalSQL, owner); err != nil {
			return err
		}
		payload.externalSQL = owner
	}
	request.ExternalSQL = payload.externalSQL.Capability(s.Op, handler.ExternalProvider, *request)
	return nil
}
