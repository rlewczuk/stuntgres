package engine

import "stuntgres/internal/engine/exec"

func (*AuthorizationView) AdmissionAuthoritySlot()     {}
func (*routineAdmissionProgram) AdmissionProgramSlot() {}

func authoritySlot(auth *AuthorizationView) exec.AdmissionAuthority {
	if auth == nil {
		return nil
	}
	return auth
}

func programSlot(program *routineAdmissionProgram) exec.AdmissionProgram {
	if program == nil {
		return nil
	}
	return program
}

// routineAuthorityView is the borrowed local DDL admission authority, or nil.
func routineAuthorityView(c *exec.Context) *AuthorizationView {
	auth, _ := c.RoutineAuthority.(*AuthorizationView)
	return auth
}

// routineProgramOf is the borrowed serial preparation footprint, or nil.
func routineProgramOf(c *exec.Context) *routineAdmissionProgram {
	program, _ := c.RoutineProgram.(*routineAdmissionProgram)
	return program
}
