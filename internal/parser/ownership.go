package parser

// AlterOwner retains the object family and role operand without resolving either
// during preparation. Existing sequence/view/schema/collation ASTs stay unchanged.
type AlterOwner struct {
	Kind     string
	Name     ObjectName
	// Number retains the NumericOnly node for large objects, without OID input
	// or catalog resolution during Prepare. Other ownership families use Name.
	Number   Value
	Target   RoleSpec
	IfExists bool
	Pos      Position
}

func (*AlterOwner) statement() {}


// ownershipNumber reproduces NumericOnly's int32 scanner-node boundary. Larger
// integer spellings remain textual nodes, including their radix/underscores;
// oidparse applies textual OID input only when the statement executes.
func ownershipNumber(tok lexeme) Value {
	text, sign := tok.text, ""
	if text[0] == '+' || text[0] == '-' {
		sign, text = text[:1], text[1:]
	}
	unsigned := tok
	unsigned.text = text
	v := numberValue(unsigned, "")
	if v.Kind == IntegerValue && v.Integer <= 2147483647 {
		if sign == "-" {
			v.Integer = -v.Integer
		}
		v.Text, v.Pos = tok.text, tok.pos
		return v
	}
	if sign == "-" {
		text = tok.text
	}
	return Value{Kind: NumericValue, Text: text, Pos: tok.pos}
}
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
