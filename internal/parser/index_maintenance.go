package parser

// Index utilities retain semantic controls until Execute. Parse/Describe never
// resolve targets or silently discard a valid physical-control request.
type IndexOption struct {
	Name      Identifier
	Namespace string
	Value     Value
	HasValue  bool
}
type DropIndex struct {
	Names      []ObjectName
	IfExists   bool
	Mode       DropMode
	Concurrent Position
	Pos        Position
}

func (*DropIndex) statement() {}

type AlterIndex struct {
	Name      ObjectName
	IfExists  bool
	Action    string
	Target    Identifier
	Partition ObjectName
	Options   []IndexOption
	ActionPos Position
	Pos       Position
}

func (*AlterIndex) statement() {}

type Reindex struct {
	Name       ObjectName
	Kind       Identifier
	Options    []IndexOption
	Concurrent Position
	Pos        Position
}

func (*Reindex) statement() {}
