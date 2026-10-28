package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// EnforceConstraintCandidate owns only the changed FK slice. The returned scan
// fact captures the genuine transition; a later disable cannot erase its work.
func EnforceConstraintCandidate(a ddl.Allocator, old *catalog.Relation, name string, negative bool) (*catalog.Relation, *catalog.ForeignKeyConstraint, error) {
	at := slices.IndexFunc(old.Constraints.ForeignKeys, func(f catalog.ForeignKeyConstraint) bool { return f.Name == name })
	if at < 0 {
		if catalog.RelationConstraintNameExists(old, name) {
			return old, nil, sqlError("42809", fmt.Sprintf(`cannot alter enforceability of constraint "%s" of relation "%s"`, name, old.Name()))
		}
		return old, nil, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	if old.Constraints.ForeignKeys[at].NotEnforced == negative {
		return old, nil, nil
	}
	next := *old
	next.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
	f := &next.Constraints.ForeignKeys[at]
	f.NotEnforced, f.NotValid = negative, negative
	if negative {
		f.TriggerOIDs = [4]ids.OID{}
		f.TriggerModes = [4]catalog.TriggerMode{}
		return &next, nil, nil
	}
	if err := AssignForeignKeyTriggerOIDs(a, f); err != nil {
		return old, nil, err
	}
	return &next, f, nil
}
