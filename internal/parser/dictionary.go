package parser

type DictionaryOption struct {
	CollationOption
	Supplied bool
}
type CreateDictionary struct {
	Name    ObjectName
	Options []DictionaryOption
	Pos     Position
}
type AlterDictionary struct {
	Name          ObjectName
	Options       []DictionaryOption
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}
type DropDictionary struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*CreateDictionary) statement() {}
func (*AlterDictionary) statement()  {}
func (*DropDictionary) statement()   {}
