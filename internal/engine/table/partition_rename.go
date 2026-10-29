package table

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// PartitionRenameOrder captures the selected breadth-first closure, with each
// parent's children ordered by OID. The caller supplies snapshot-visible children.
// Callers lock this whole queue, then validate descendants before the root.
func PartitionRenameOrder(view catalog.Read, root *catalog.Relation, selectChildren func(*catalog.Relation) []ids.ObjectAddress) []*catalog.Relation {
	targets := []*catalog.Relation{root}
	seen := map[ids.ObjectAddress]bool{root.Address(): true}
	var children []ids.ObjectAddress
	for i := 0; i < len(targets); i++ {
		children = append(children[:0], selectChildren(targets[i])...)
		slices.SortFunc(children, func(a, b ids.ObjectAddress) int {
			return cmp.Compare(a.ObjectID, b.ObjectID)
		})
		for _, address := range children {
			if !seen[address] {
				seen[address] = true
				targets = append(targets, view.MustRelation(address))
			}
		}
	}
	return targets
}

func PartitionRenameOnly(children []ids.ObjectAddress, column string) error {
	if len(children) != 0 {
		return sqlError("42P16", fmt.Sprintf(`inherited column "%s" must be renamed in child tables too`, column))
	}
	return nil
}

// RenamePartitionColumn changes the matching local physical slot, never a
// parent's slot/attribute number. An attached addressed target has no selected
// parent contribution, so inherited eligibility precedes destination collision.
func RenamePartitionColumn(old *catalog.Relation, column, destination string, addressed bool) (*catalog.Relation, error) {
	if catalog.IsSystemColumn(column) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot rename system column "%s"`, column))
	}
	if _, exists := old.Index[column]; !exists {
		return nil, sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, column))
	}
	if addressed && old.Membership != nil {
		return nil, sqlError("42P16", fmt.Sprintf(`cannot rename inherited column "%s"`, column))
	}
	next, err := RenameColumnCandidate(old, column, destination)
	if err == nil {
		err = BumpAlterRevision(old, next)
	}
	return next, err
}
