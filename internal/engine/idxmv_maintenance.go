package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// StageMaintenance stages ALTER INDEX ATTACH PARTITION and REINDEX, and the
// observations every index utility publishes against: the parents and index
// sets the grant saw, and every observed descriptor as a domain base. REINDEX
// records its rebuilds and makes an index that is not ready, valid and live
// so again. RENAME and DROP stay with the caller.
func StageMaintenance(h index.Host, g *index.MaintenanceGrant, stmt parser.Statement) error {
	st := h.IndexStage()
	st.Parents = make(map[ids.ObjectAddress]*catalog.Relation, len(g.Parents))
	for _, ref := range g.Parents {
		st.Parents[ref.Address] = g.Descriptors[ref.Address]
		h.AddDomainBase(g.Descriptors[ref.Address])
	}
	st.Sets = g.IndexSets
	for address, observed := range g.Descriptors {
		if observed.SQLIndex != nil {
			h.AddDomainBase(g.Descriptors[address])
		}
	}
	if _, reindex := stmt.(*parser.Reindex); reindex {
		st.Rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(g.Indexes))
		for _, ref := range g.Indexes {
			st.Rebuilds[ref.Address] = g.Descriptors[ref.Address]
			original := g.Descriptors[ref.Address]
			if !original.SQLIndex.Ready || !original.SQLIndex.Valid || !original.SQLIndex.Live {
				next, definition := *original, *original.SQLIndex
				definition.Ready, definition.Valid, definition.Live = true, true, true
				next.SQLIndex = &definition
				if next.Revision() == ids.DefinitionRevision(^uint64(0)) {
					return fmt.Errorf("stuntgres: index revision exhausted")
				}
				next.ObjectRevision++
				if err := h.Stage().Replace(&next); err != nil {
					return err
				}
			}
		}
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "attach" {
		parent := g.Descriptors[g.Targets[0].Address]
		child := g.Descriptors[g.Attach.Address]
		return StagePartitionIndexAttach(h, parent, child)
	}
	return nil
}

// ValidateMaintenance checks a stage's index observations against the exact
// current catalog before any work is prepared; the publication loop also
// fences that catalog at install. replacedParent is the original of a
// materialized view the stage replaces, or zero.
func ValidateMaintenance(st index.Stage, base catalog.Read, current *catalog.State, replacedParent ids.ObjectAddress) error {
	if (len(st.Parents) > 0 || len(st.Rebuilds) > 0 || len(st.Sets) > 0) && current.Epoch() != base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for address, observed := range st.Parents {
		if current.Get(address) != observed {
			return catalog.Conflict{}
		}
	}
	for address, observed := range st.Rebuilds {
		if observed == nil || observed.SQLIndex == nil || current.Get(address) != observed || base.Get(address) != observed {
			return catalog.Conflict{}
		}
	}
	for parent, count := range st.Sets {
		actual := 0
		for address, descriptor := range current.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || d.SQLIndex == nil || d.SQLIndex.Parent != parent {
				continue
			}
			// Replacement observes the existing complete index set; it does
			// not request REINDEX or replace any index descriptor. A zero
			// replacedParent never equals a parent.
			if st.Rebuilds[address] == nil && (replacedParent != parent || base.Get(address) != d) {
				return catalog.Conflict{}
			}
			actual++
		}
		if actual != count {
			return catalog.Conflict{}
		}
	}
	return nil
}
