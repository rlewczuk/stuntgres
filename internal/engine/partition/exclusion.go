package partition

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Only selected EXCLUDE declarations use the public recursive ADD route.
// Private replay origins for other constraint kinds retain their own fences.
func ExclusionAddActions(raw *parser.AlterTable) bool {
	if len(raw.Actions) == 0 {
		return false
	}
	for _, action := range raw.Actions {
		if action.Action != parser.AddExclusion || action.Exclusion == nil {
			return false
		}
	}
	return true
}

// StageExclusions stages the EXCLUDE constraints of raw on the partitioned
// relation, recursing into its partitions unless raw is ONLY. It runs with
// Engine.mu held and never waits.
func StageExclusions(h index.Host, auth ddl.Authority, input sqltype.InputEnvironment, relation *catalog.Relation, raw *parser.AlterTable) error {
	next := *relation
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	next.ObjectRevision = revision
	next.Constraints.Exclusion = slices.Clone(relation.Constraints.Exclusion)
	for _, action := range raw.Actions {
		if err := index.StageExclusion(h, auth, input, &next, action.Exclusion, !raw.Only); err != nil {
			return err
		}
	}
	return nil
}
