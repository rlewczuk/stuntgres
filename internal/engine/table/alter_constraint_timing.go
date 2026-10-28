package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
)

// AlterConstraintTimingCandidate detaches only the FK slice when its persistent
// defaults change. The caller checks pending relation events before selection,
// including no-ops; transaction mode overrides are not definition state.
func AlterConstraintTimingCandidate(old *catalog.Relation, name string, deferrable, initiallyDeferred bool) (*catalog.Relation, error) {
	at := slices.IndexFunc(old.Constraints.ForeignKeys, func(f catalog.ForeignKeyConstraint) bool { return f.Name == name })
	if at < 0 {
		if catalog.RelationConstraintNameExists(old, name) {
			return old, sqlError("42809", fmt.Sprintf(`constraint "%s" of relation "%s" is not a foreign key constraint`, name, old.Name()))
		}
		return old, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	if initiallyDeferred && !deferrable {
		return old, sqlError("42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE")
	}
	previous := old.Constraints.ForeignKeys[at]
	if previous.Deferrable == deferrable && previous.InitiallyDeferred == initiallyDeferred {
		return old, nil
	}
	next := *old
	next.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
	f := &next.Constraints.ForeignKeys[at]
	f.Deferrable, f.InitiallyDeferred = deferrable, initiallyDeferred
	return &next, nil
}
