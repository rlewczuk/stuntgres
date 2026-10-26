package catalog

// RoutineInputNames owns the released names of a builtin's declared input
// slots. Fixed outputs and checked SQL formals retain their complete own names.
type RoutineInputNames struct{ names []string }

func NewRoutineInputNames(names ...string) *RoutineInputNames {
	return &RoutineInputNames{names: append([]string(nil), names...)}
}
func (n *RoutineInputNames) Len() int {
	if n == nil {
		return 0
	}
	return len(n.names)
}
func (n *RoutineInputNames) At(i int) string { return n.names[i] }
