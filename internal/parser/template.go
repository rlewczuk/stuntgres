package parser

// Template callbacks retain the same positioned, ordered utility values as
// dictionary options; exact signatures are resolved by the catalog owner.
type CreateTemplate struct {
	Name ObjectName
	Options []DictionaryOption
	Pos Position
}
type AlterTemplate struct {
	Name ObjectName
	Action string
	Target Identifier
	Pos Position
}
type DropTemplate struct {
	Names []ObjectName
	IfExists bool
	Mode DropMode
	Pos Position
}
func (*CreateTemplate) statement() {}
func (*AlterTemplate) statement() {}
func (*DropTemplate) statement() {}
