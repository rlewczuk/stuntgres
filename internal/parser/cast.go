package parser

// Cast identity is the ordered pair of endpoint types, not a schema name.
type CreateCast struct {
	Source, Target  TypeName
	Function        GrantRoutineTarget
	Method, Context string
	Pos             Position
}

func (*CreateCast) statement() {}

type DropCast struct {
	Source, Target TypeName
	IfExists       bool
	Mode           DropMode
	Pos            Position
}

func (*DropCast) statement() {}
