package table

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ValidateAlterTablespaceMove rejects moving an ordinary relation into
// pg_global.
func ValidateAlterTablespaceMove(destination ids.ObjectRef) error {
	if destination.Address.ObjectID == 1664 {
		return sqlError("22023", "only shared relations can be placed in pg_global tablespace")
	}
	return nil
}

// TablespaceOnlyActions reports whether a non-empty ALTER TABLE action list
// holds only SET TABLESPACE actions.
func TablespaceOnlyActions(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if action.Action != parser.SetTablespace {
			return false
		}
	}
	return true
}
