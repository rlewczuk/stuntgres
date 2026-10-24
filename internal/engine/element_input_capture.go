package engine

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Capture recursive catalog input from one coherent authorization snapshot.
// Frozen expressions and newly bound expressions share these same owners.
func (e *Engine) captureElementInput(auth AuthorizationView, op *session.Operation, input *sqltype.InputEnvironment) error {
	var err error
	if input.Regclass == nil {
		input.Regclass, err = captureRegclassInput(auth)
		if err != nil {
			return err
		}
	}
	if input.Regtype == nil {
		input.Regtype, err = captureRegtypeInput(auth, op)
		if err != nil {
			return err
		}
	}
	if input.Regproc == nil {
		input.Regproc, err = captureRoutineInput(auth, input.Regtype.(regobj.TypeInput))
	}
	if input.Regrole == nil {
		input.Regrole = regobj.RoleInput{Roles: auth.Roles}
	}
	if err == nil && input.Operator == nil {
		input.Operator, err = captureOperatorInput(auth, op)
	}
	if err == nil && (input.Regcollation == nil || input.Regnamespace == nil) {
		var resolver regobj.ObjectInput
		resolver, err = captureRegObjectInput(auth)
		if err == nil {
			input.Regcollation, input.Regnamespace = resolver, resolver
		}
	}
	if err == nil && input.RegTS == nil {
		input.RegTS, err = captureRegTSInput(auth)
	}
	return err
}
