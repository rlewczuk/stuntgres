package engine

import (
	"stuntgres/internal/parser"
)

func partitionCheckReplayActions(raw *parser.AlterTable) bool {
	if raw == nil || len(raw.Actions) == 0 {
		return false
	}
	for _, action := range raw.Actions {
		if action.Action != parser.AddCheck || action.Check == nil || !action.Check.NotValid {
			return false
		}
	}
	return true
}
