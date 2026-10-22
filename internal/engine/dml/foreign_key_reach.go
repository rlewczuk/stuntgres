package dml

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// ForeignKeyReach is the FK jobs that the staged rows and deletions reach in
// view, and the relation lock mode of each job's child and parent. Without a
// foreign key touching a staged relation it returns no jobs and no modes.
// The jobs are in catalog order; the caller sorts them.
func ForeignKeyReach(view catalog.Read, domain *ids.Domain, rows map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) ([]ForeignKeyJob, map[ids.ObjectRef]lock.Mode, error) {
	relevant := false
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if len(rows[child.Address()]) != 0 || deletes[child.Address()] != nil || len(rows[f.Target]) != 0 || deletes[f.Target] != nil {
				relevant = true
				break
			}
		}
		if relevant {
			break
		}
	}
	if !relevant {
		return nil, nil, nil
	}
	// Reachability is by actual transition direction, not an executable global
	// registry. Only the existing catalog is consulted while capturing authority.
	directions := make(map[ids.ObjectAddress]uint8)
	changed := make(map[ids.ObjectAddress]bool)
	for a, staged := range rows {
		if len(staged) != 0 {
			changed[a] = true
			directions[a] |= 2
		}
	}
	for a, root := range deletes {
		if root != nil {
			changed[a] = true
			directions[a] |= 1
		}
	}
	again := true
	for again {
		again = false
		for _, descriptor := range view.Descriptors() {
			child, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, f := range child.Constraints.ForeignKeys {
				parentDirections := directions[f.Target]
				next := directions[child.Address()]
				if parentDirections&1 != 0 && f.DeleteAction >= parser.ForeignKeyCascade {
					if f.DeleteAction == parser.ForeignKeyCascade {
						next |= 1
					} else {
						next |= 2
					}
				}
				if parentDirections&2 != 0 && f.UpdateAction >= parser.ForeignKeyCascade {
					next |= 2
				}
				if next != directions[child.Address()] {
					directions[child.Address()] = next
					changed[child.Address()] = true
					again = true
				}
			}
		}
	}
	var jobs []ForeignKeyJob
	modes := make(map[ids.ObjectRef]lock.Mode)
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if !changed[child.Address()] && !changed[f.Target] {
				continue
			}
			parent, pok := view.Relation(f.Target)
			index, iok := view.Relation(f.Index)
			if !pok || !iok {
				return nil, nil, ids.ErrCatalogReplaced
			}
			jobs = append(jobs, ForeignKeyJob{Child: child, Parent: parent, Index: index, Definition: f})
			for _, a := range []ids.ObjectAddress{child.Address(), parent.Address()} {
				ref := domain.Ref(view.Epoch(), a)
				if modes[ref] == 0 {
					modes[ref] = lock.RowShare
				}
			}
			d := directions[f.Target]
			if d&1 != 0 && f.DeleteAction >= parser.ForeignKeyCascade || d&2 != 0 && f.UpdateAction >= parser.ForeignKeyCascade {
				modes[domain.Ref(view.Epoch(), child.Address())] = lock.RowExclusive
			}
		}
	}
	return jobs, modes, nil
}
