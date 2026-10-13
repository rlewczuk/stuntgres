package parser

// Encoding operands retain Sconst bytes; they are not NAME-clipped identifiers.
type CreateConversion struct {
	Name                ObjectName
	Source, Destination string
	Function            ObjectName
	Default             bool
	Pos                 Position
}

func (*CreateConversion) statement() {}

type AlterConversion struct {
	Name          ObjectName
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}

func (*AlterConversion) statement() {}

type DropConversion struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropConversion) statement() {}
