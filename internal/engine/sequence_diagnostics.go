package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func sequenceKind(d *catalog.Relation) error {
	if d == nil {
		return fmt.Errorf("stuntgres: missing sequence descriptor")
	}
	if d.RelationKind != catalog.SequenceRelation {
		if d.RelationKind == catalog.IndexRelation {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.Name()), Detail: "This operation is not supported for indexes."}
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.Name()), Detail: "This operation is not supported for tables."}
	}
	return nil
}

func sequenceLinkedError(d *catalog.Relation, table *catalog.Relation, move bool) error {
	msg := fmt.Sprintf(`cannot change owner of sequence "%s"`, d.Name())
	if move {
		msg = "cannot move an owned sequence into another schema"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: msg, Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, d.Name(), table.Name())}
}

func identityInsertError(column string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "428C9", Message: fmt.Sprintf(`cannot insert a non-DEFAULT value into column "%s"`, column), Detail: fmt.Sprintf(`Column "%s" is an identity column defined as GENERATED ALWAYS.`, column), Hint: "Use OVERRIDING SYSTEM VALUE to override."}
}

func identitySequenceDropError(read catalog.Read, sequence *catalog.Relation) error {
	address := sequence.Sequence.Owned
	address.SubID = 0
	table := read.MustRelation(address)
	column := ""
	for slot, attribute := range table.Attributes {
		if attribute == sequence.Sequence.Owned.SubID {
			column = table.Columns[slot].Name
			break
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop sequence %s because column %s of table %s requires it", sequence.Name(), column, table.Name()), Hint: fmt.Sprintf("You can drop column %s of table %s instead.", column, table.Name())}
}

func identitySequenceDefinition(descriptor catalog.Descriptor, table *catalog.Relation, attribute int32) (*catalog.Relation, error) {
	sequence, ok := descriptor.(*catalog.Relation)
	owned := table.Address()
	owned.SubID = attribute
	if !ok || sequence == nil || sequence.Sequence == nil || !sequence.Sequence.Identity || sequence.Sequence.Owned != owned {
		return nil, ids.ErrCatalogReplaced
	}
	return sequence, nil
}

// SERIAL is declaration syntax, never a catalog type or identity column.
func serialTypeName(name parser.TypeName) string {
	if name.Schema != nil {
		return ""
	}
	switch name.Name {
	case "smallserial", "serial2":
		return "int2"
	case "serial", "serial4":
		return "int4"
	case "bigserial", "serial8":
		return "int8"
	}
	return ""
}
