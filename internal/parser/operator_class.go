package parser

// CreateOperatorClass retains declaration order, including STORAGE, for utility admission.
type CreateOperatorClass struct {
	Name      ObjectName
	Default   bool
	InputType TypeName
	Method    Identifier
	Family    ObjectName
	Items     []OperatorFamilyItem
	Pos       Position
}

func (*CreateOperatorClass) statement() {}

type AlterOperatorClass struct {
	Name          ObjectName
	Method        Identifier
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}

func (*AlterOperatorClass) statement() {}

type DropOperatorClass struct {
	Name     ObjectName
	Method   Identifier
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropOperatorClass) statement() {}
