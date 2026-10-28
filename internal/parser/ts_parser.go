package parser

// Parser options retain every occurrence and its location in written order.
type CreateParser struct {
	Name    ObjectName
	Options []ConfigurationOption
	Pos     Position
}
type AlterParser struct {
	Name   ObjectName
	Action string
	Target Identifier
	Pos    Position
}
type DropParser struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*CreateParser) statement() {}
func (*AlterParser) statement()  {}
func (*DropParser) statement()   {}
