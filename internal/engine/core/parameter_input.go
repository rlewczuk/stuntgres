package core

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// captureFormalInputLocked is shared by ordinary binding and direct fastpath
// invocation. The caller holds Engine.mu; the captured readers own immutable
// catalog/role authority and actual codec input later runs without that mutex.
func (s *StatementScope) captureFormalInputLocked(auth AuthorizationView, types []sqltype.Usage) (err error) {
	e, op := s.Engine(), s.Op
	if s.Input.XMLInput == nil {
		s.Input.XMLInput = session.XMLInput{Op: op}
	}
	for _, usage := range types {
		if sqltype.IsFTS(usage.Type.BaseType().ID()) && s.Input.FTS == nil {
			s.Input.FTS = session.FTSInput{Op: op}
		}
		if usage.Type.ContainsRegclass() && s.Input.Regclass == nil {
			s.Input.Regclass, err = captureRegclassInput(auth)
			if err != nil {
				return err
			}
		}
		if (usage.Type.ContainsRegtype() || usage.Type.ContainsRegproc()) && s.Input.Regtype == nil {
			s.Input.Regtype, err = CaptureRegtypeInput(auth, op)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegrole() && s.Input.Regrole == nil {
			s.Input.Regrole = regobj.RoleInput{Roles: auth.Roles}
		}
		if usage.Type.ContainsOperatorAlias() && s.Input.Operator == nil {
			s.Input.Operator, err = captureOperatorInput(auth, op)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegproc() && s.Input.Regproc == nil {
			s.Input.Regproc, err = CaptureRoutineInput(auth, s.Input.Regtype.(regobj.TypeInput))
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsRegObject() && (s.Input.Regcollation == nil || s.Input.Regnamespace == nil) {
			input, e := captureRegObjectInput(auth)
			if e != nil {
				return e
			}
			s.Input.Regcollation, s.Input.Regnamespace = input, input
		}
		if usage.Type.ContainsRegTS() && s.Input.RegTS == nil {
			s.Input.RegTS, err = CaptureRegTSInput(auth)
			if err != nil {
				return err
			}
		}
		if usage.Type.ContainsACLItem() && s.Input.ACLItem == nil {
			s.Input.ACLItem = e.CaptureACLItemInput(auth, op)
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
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = s.captureFormalInputLocked(auth, types)
	}
	if err == nil {
		err = captureEnumOperationInput(auth, &s.Input)
	}
	if err == nil {
		s.Domains.InputCatalog = auth.Catalog
	}
	e.Mu.Unlock()
	if err == nil {
		s.Input.LockedDomainInput, s.Input.DomainChecks = s, s
	}
	return err
}
