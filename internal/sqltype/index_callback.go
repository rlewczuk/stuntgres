package sqltype

// IndexSupportBoundary is borrowed only at an actual selected support kernel.
// Support slots are roles, never an instruction to preflight every member.
type IndexSupportBoundary interface {
	EnterIndexSupport(IndexSupport, uint16) (RoutineBodyToken, error)
}

func enterIndexSupport(input InputEnvironment, support IndexSupport, slot uint16) (RoutineBodyToken, error) {
	if input.IndexSupport == nil {
		return RoutineBodyToken{}, nil
	}
	return input.IndexSupport.EnterIndexSupport(support, slot)
}
