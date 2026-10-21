package engine

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// captureFormalInputLocked is shared by ordinary binding and direct fastpath
// invocation. The caller holds Engine.mu; the captured readers own immutable
// catalog/role authority and actual codec input later runs without that mutex.
func (s *StatementScope) captureFormalInputLocked(auth AuthorizationView, types []sqltype.Usage) (err error) {
	e, op := s.engine(), s.op
	for _, usage := range types {
		if sqltype.IsFTS(usage.Type.BaseType().ID()) && s.input.FTS == nil {
			s.input.FTS = capturedFTSInput{op: op}
		}
		if usage.Type.ContainsRegclass() && s.input.Regclass == nil {
			s.input.Regclass, err = e.captureRegclassInput(auth)
			if err != nil {
				return err
			}
		}
		if (usage.Type.ContainsRegtype() || usage.Type.ContainsRegproc()) && s.input.Regtype == nil {
			s.input.Regtype, err = e.captureRegtypeInput(auth, op)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegrole() && s.input.Regrole == nil {
			s.input.Regrole = regobj.RoleInput{Roles: auth.Roles}
		}
		if usage.Type.ContainsOperatorAlias() && s.input.Operator == nil {
			s.input.Operator, err = e.captureOperatorInput(auth, op)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegproc() && s.input.Regproc == nil {
			s.input.Regproc, err = e.captureRoutineInput(auth, s.input.Regtype.(capturedRegtypeInput))
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegObject() && (s.input.Regcollation == nil || s.input.Regnamespace == nil) {
			input, e := e.captureRegObjectInput(auth)
			if e != nil {
				return e
			}
			s.input.Regcollation, s.input.Regnamespace = input, input
		}
		if usage.Type.ContainsRegTS() && s.input.RegTS == nil {
			s.input.RegTS, err = e.captureRegTSInput(auth)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsACLItem() && s.input.ACLItem == nil {
			s.input.ACLItem = e.captureACLItemInput(auth, op)
		}
	}
	return nil
}

// Untransformed wire utilities may retain explicitly declared unused formals.
// They still receive the same catalog-aware input/domain authority as DML.
func (s *StatementScope) prepareProtocolUtilityInput(types []sqltype.Usage) error {
	if len(types) == 0 {
		return nil
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil {
		err = s.captureFormalInputLocked(auth, types)
	}
	if err == nil {
		err = e.captureEnumOperationInput(auth, &s.input)
	}
	if err == nil {
		s.domains.inputCatalog = auth.Catalog
	}
	e.mu.Unlock()
	if err == nil {
		s.input.LockedDomainInput, s.input.DomainChecks = s, s
	}
	return err
}
