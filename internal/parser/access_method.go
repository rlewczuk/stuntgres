package parser

// Access methods have one global (not schema-qualified) SQL name.
type CreateAccessMethod struct {
	Name       Identifier
	MethodKind string
	Handler    ObjectName
	Pos        Position
}

func (*CreateAccessMethod) statement() {}

type DropAccessMethod struct {
	Name     Identifier
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropAccessMethod) statement() {}
