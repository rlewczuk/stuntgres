package engine

// The routine admission slots of evalAdmission are opaque to the runtime: it
// copies them with the context (the borrowed pointer, never the pointee) and
// only engine helpers look inside. A nil pointer is stored as an untyped nil.
type admissionAuthority interface{ AdmissionAuthoritySlot() }
type admissionProgram interface{ AdmissionProgramSlot() }

func (*AuthorizationView) AdmissionAuthoritySlot()     {}
func (*routineAdmissionProgram) AdmissionProgramSlot() {}

func authoritySlot(auth *AuthorizationView) admissionAuthority {
	if auth == nil {
		return nil
	}
	return auth
}

func programSlot(program *routineAdmissionProgram) admissionProgram {
	if program == nil {
		return nil
	}
	return program
}

// routineAuthorityView is the borrowed local DDL admission authority, or nil.
func routineAuthorityView(c *EvalContext) *AuthorizationView {
	auth, _ := c.routineAuthority.(*AuthorizationView)
	return auth
}

// routineProgramOf is the borrowed serial preparation footprint, or nil.
func routineProgramOf(c *EvalContext) *routineAdmissionProgram {
	program, _ := c.routineProgram.(*routineAdmissionProgram)
	return program
}
