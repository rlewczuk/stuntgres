package engine

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func alterTableLockMode(raw *parser.AlterTable) lock.Mode {
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			return lock.AccessExclusive
		}
	}
	if len(raw.Actions) == 0 {
		return lock.AccessExclusive
	}
	return lock.ShareRowExclusive
}
