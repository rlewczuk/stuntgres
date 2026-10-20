package sequence

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ParseOwnedBy splits an OWNED BY target into its table name and column;
// none reports OWNED BY NONE. The engine resolves and locks the table.
func ParseOwnedBy(raw parser.ObjectName) (table parser.ObjectName, column string, none bool, err error) {
	if len(raw.Parts) == 1 && raw.Parts[0].Text == "none" {
		return parser.ObjectName{}, "", true, nil
	}
	if len(raw.Parts) < 2 || len(raw.Parts) > 3 {
		return parser.ObjectName{}, "", false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid OWNED BY option", Hint: "Specify OWNED BY table.column or OWNED BY NONE."}
	}
	return parser.ObjectName{Parts: raw.Parts[:len(raw.Parts)-1]}, raw.Parts[len(raw.Parts)-1].Text, false, nil
}

// CheckOwnedBy validates the OWNED BY table of sequence d, read after the
// table's lock grant, and returns the owning column's address.
func CheckOwnedBy(table, d *catalog.Relation, column string) (ids.ObjectAddress, error) {
	if table.RelationKind != catalog.TableRelation && table.RelationKind != catalog.PartitionedRelation {
		return ids.ObjectAddress{}, sqlError("42809", fmt.Sprintf(`referenced relation "%s" is not a table`, table.Name()))
	}
	if table.Owner != d.Owner {
		return ids.ObjectAddress{}, sqlError("55000", "sequence must have same owner as table it is linked to")
	}
	if table.Namespace() != d.Namespace() {
		return ids.ObjectAddress{}, sqlError("55000", "sequence must be in same schema as table it is linked to")
	}
	slot, ok := table.Index[column]
	if !ok {
		return ids.ObjectAddress{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, table.Name()))
	}
	a := table.Address()
	a.SubID = table.Attributes[slot]
	return a, nil
}
