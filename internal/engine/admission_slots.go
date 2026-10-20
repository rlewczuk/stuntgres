package engine

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
	auth, _ := c.RoutineAuthority.(*AuthorizationView)
	return auth
}

// routineProgramOf is the borrowed serial preparation footprint, or nil.
func routineProgramOf(c *EvalContext) *routineAdmissionProgram {
	program, _ := c.RoutineProgram.(*routineAdmissionProgram)
	return program
}
