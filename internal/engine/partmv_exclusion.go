package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

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
