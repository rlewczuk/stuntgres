package parser

// AlterOwner retains the object family and role operand without resolving either
// during preparation. Existing sequence/view/schema/collation ASTs stay unchanged.
type AlterOwner struct {
	Kind     string
	Name     ObjectName
	Target   RoleSpec
	IfExists bool
	Pos      Position
}

func (*AlterOwner) statement() {}

type ReassignOwned struct {
	Sources []RoleSpec
	Target  RoleSpec
	Pos     Position
}

func (*ReassignOwned) statement() {}

type DropOwned struct {
	Sources []RoleSpec
	Mode    DropMode
	Pos     Position
}

func (*DropOwned) statement() {}
