package parser

// CreateFunction is the bounded SQL text-body declaration. Body parameters are
// private routine inputs, never parameters of the enclosing CREATE statement.
type CreateFunction struct {
	Name      ObjectName
	Arguments []GrantRoutineArgument
	Result    TypeName
	Language  Identifier
	Body      string
	BodyPos   Position
	Pos       Position
}

func (*CreateFunction) statement() {}

// DropRoutine identifies catalog signatures, never call-site overload coercions.
type DropRoutine struct {
	Kind     string
	Targets  []GrantRoutineTarget
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropRoutine) statement() {}

// DROP notices retain PostgreSQL's alias-normalized TypeName syntax, unlike
// canonical catalog signature diagnostics. Reuse the regtype syntax authority.
func dropRoutineTargets(targets []GrantRoutineTarget) []GrantRoutineTarget {
	for i := range targets {
		for j := range targets[i].Arguments {
			targets[i].Arguments[j].Type = standaloneType(targets[i].Arguments[j].Type)
		}
	}
	return targets
}
