package engine

// The routine admission slots of evalAdmission are opaque to the runtime: it
// copies them with the context (the borrowed pointer, never the pointee) and
// only engine helpers look inside. A nil pointer is stored as an untyped nil.
type admissionAuthority interface{ admissionAuthority() }
type admissionProgram interface{ admissionProgram() }

func (*AuthorizationView) admissionAuthority()     {}
func (*routineAdmissionProgram) admissionProgram() {}

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
func (c *EvalContext) routineAuthorityView() *AuthorizationView {
	auth, _ := c.routineAuthority.(*AuthorizationView)
	return auth
}

// routineAdmissionProgram is the borrowed serial preparation footprint, or nil.
func (c *EvalContext) routineAdmissionProgram() *routineAdmissionProgram {
	program, _ := c.routineProgram.(*routineAdmissionProgram)
	return program
}
