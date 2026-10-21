package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// CheckReplayActions reports whether a replayed ALTER TABLE only adds NOT
// VALID CHECK constraints.
func CheckReplayActions(raw *parser.AlterTable) bool {
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

// ReplayTeardown names the private parent and child a Restore DETACH replay
// tears down.
type ReplayTeardown struct{ Child, Parent ids.ObjectRef }

// UniqueReplay is the leased Restore target of a partitioned UNIQUE replay.
type UniqueReplay struct {
	Target      *catalog.Relation
	Syntax      *parser.AlterTable
	Definition  *parser.UniqueConstraint
	Snapshot    parser.UniqueConstraint
	Descriptors []*catalog.Relation
}

// UsingIndexReplay is the leased Restore target of a partitioned
// constraint USING INDEX replay.
type UsingIndexReplay struct {
	Target, Index *catalog.Relation
	Syntax        *parser.AlterTable
}
