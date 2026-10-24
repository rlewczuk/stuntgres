package table

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// CheckCreateShape returns the first CREATE TABLE shape error once the
// persistence is settled: TEMP partitioning, ON COMMIT on a permanent table,
// an existing relation name, and a partitioned inheritance child. existing
// reports an existing name under IF NOT EXISTS, which skips creation with a
// notice instead. Explicit owners use the ordinary transaction journal.
func CheckCreateShape(s *parser.CreateTable, temporary, exists bool) (existing bool, err error) {
	if temporary && s.PartitionOf != nil {
		return false, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", s.PartitionOf.Pos)
	}
	if temporary && s.PartitionBy != nil {
		return false, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", s.PartitionBy.Pos)
	}
	if !temporary && s.OnCommit != parser.OnCommitUnspecified {
		return false, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
	}
	if exists {
		if s.IfNotExists {
			return true, nil
		}
		return false, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, s.Name.Parts[len(s.Name.Parts)-1].Text))
	}
	if s.PartitionBy != nil && len(s.Parents) != 0 {
		return false, sqlError("42P17", "cannot create partitioned table as inheritance child")
	}
	return false, nil
}
