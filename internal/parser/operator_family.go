package parser

// OperatorFamilyItem retains explicit association separately from the referenced
// function/operator signature; omission matters to released ADD admission.
type OperatorFamilyItem struct {
	Function    bool
	Storage     *TypeName
	Number      string
	Name        ObjectName
	Arguments   []TypeName
	Association []TypeName
	Signature   bool
	Purpose     string
	SortFamily  ObjectName
	Pos         Position
}
type CreateOperatorFamily struct {
	Name   ObjectName
	Method Identifier
	Pos    Position
}

func (*CreateOperatorFamily) statement() {}

type AlterOperatorFamily struct {
	Name          ObjectName
	Method        Identifier
	Action        string
	Items         []OperatorFamilyItem
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}

func (*AlterOperatorFamily) statement() {}

type DropOperatorFamily struct {
	Name     ObjectName
	Method   Identifier
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropOperatorFamily) statement() {}
