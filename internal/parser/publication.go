package parser

// Publication preserves ordered utility operands and expression positions.
// Member groups retain their introducers; omitted lists and filters stay absent.
type Publication struct {
	Action        string
	Name, Target  Identifier
	Names         []Identifier
	OwnerIdentity bool
	AllTables     bool
	Members       []PublicationMember
	Options       []DictionaryOption
	IfExists      bool
	Mode          DropMode
	Pos           Position
}

func (*Publication) statement() {}

type PublicationMember struct {
	Kind          string // Effective introducer, including comma continuations.
	Table         ObjectName
	Schema        Identifier
	CurrentSchema bool
	Only, Star    bool
	Columns       []Identifier
	Filter        Expr
}

// A comma has one grammar owner. Continuations inherit their preceding
// introducer, avoiding ambiguous nested table/schema lists.
func appendPublicationMember(l sqlLexer, members []PublicationMember, next PublicationMember) ([]PublicationMember, bool) {
	if next.Kind == "" {
		next.Kind = members[len(members)-1].Kind
		if next.Kind == "schema" && !next.CurrentSchema {
			if len(next.Table.Parts) != 1 || next.Only || next.Star || next.Columns != nil || next.Filter != nil {
				l.(*lexer).syntax(lexeme{text: next.Table.Parts[0].Text, pos: next.Table.Parts[0].Pos})
				return nil, false
			}
			next.Schema = next.Table.Parts[0]
			next.Table = ObjectName{}
		} else if next.Kind == "table" && next.CurrentSchema {
			l.(*lexer).syntax(lexeme{text: next.Schema.Text, pos: next.Schema.Pos})
			return nil, false
		}
	}
	return append(members, next), true
}
