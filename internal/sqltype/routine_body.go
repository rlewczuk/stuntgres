package sqltype

// RoutineBodyOwner restores one balanced lexical invocation, not SQL identity
// or catalog authority. The actual physical-session owner supplies it.
type RoutineBodyOwner interface{ LeaveRoutineBody(depth int, stamp uint64) }

// RoutineBodyToken owns one entered lexical scope. The zero value needs no
// cleanup. It retains neither input values, an evaluator nor a callback closure.
type RoutineBodyToken struct {
	owner RoutineBodyOwner
	depth int
	stamp uint64
}

func NewRoutineBodyToken(owner RoutineBodyOwner, depth int, stamp uint64) RoutineBodyToken {
	return RoutineBodyToken{owner: owner, depth: depth, stamp: stamp}
}
func (t RoutineBodyToken) Active() bool { return t.owner != nil }
func (t RoutineBodyToken) Leave() {
	if t.owner != nil {
		t.owner.LeaveRoutineBody(t.depth, t.stamp)
	}
}
