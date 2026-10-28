package parser

// Subscription retains ordered options and names; preparation is syntax-only.
type Subscription struct {
	Action        string
	Name          Identifier
	Target        Identifier
	OwnerIdentity bool
	Connection    string
	Publications  []Identifier
	Options       []DictionaryOption
	IfExists      bool
	Mode          DropMode
	Pos           Position
}

func (*Subscription) statement() {}
