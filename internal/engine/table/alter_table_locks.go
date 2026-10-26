package table

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func AlterTableLockMode(raw *parser.AlterTable) lock.Mode {
	if len(raw.Actions) == 0 {
		return lock.AccessExclusive
	}
	mode := lock.ShareUpdateExclusive
	for _, action := range raw.Actions {
		if RelationOptionsAction(action.Action) {
			for _, option := range action.Options {
				if option.Name.Text == "user_catalog_table" {
					return lock.AccessExclusive
				}
			}
		} else if action.Action == parser.AddForeignKey {
			mode = lock.ShareRowExclusive
		} else if action.Action == parser.ValidateConstraint {
			// Validation never weakens a stronger companion grant.
		} else {
			return lock.AccessExclusive
		}
	}
	return mode
}
