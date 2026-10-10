package parser

// Options retain their spelling and location; semantic validation belongs to
// utility execution, so Prepare never loads a provider or publishes an object.
type CollationOption struct {
	Name    Identifier
	Value   string
	Source  *ObjectName
	Numeric bool
}

type CreateCollation struct {
	Name        ObjectName
	From        *ObjectName
	Options     []CollationOption
	IfNotExists bool
	Pos         Position
}

func (*CreateCollation) statement() {}

type AlterCollation struct {
	Name          ObjectName
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}

func (*AlterCollation) statement() {}

type DropCollation struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropCollation) statement() {}
