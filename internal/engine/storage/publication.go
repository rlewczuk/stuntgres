package storage

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Publication names the private stores a catalog candidate installs beside
// the committed relations. The root fills it from its stage under Engine.mu;
// it is a value and takes no lock.
type Publication struct {
	// Initial holds the private storage of new query-derived tables.
	Initial map[ids.ObjectAddress]*Relation
	// Shapes holds the planned heap replacements.
	Shapes map[ids.ObjectAddress]RowShape
	// Refresh is a materialized view's refreshed store, installed at
	// Refreshed; nil when the candidate refreshes nothing.
	Refreshed ids.ObjectAddress
	Refresh   *Relation
}

// CandidateRelations returns the relation map the candidate publishes: a copy
// of committed without staged removals, an initial (or empty) store for each
// new stored relation, then every heap replacement and the refreshed store.
func (p Publication) CandidateRelations(committed map[ids.ObjectAddress]*Relation, staged *catalog.Candidate) map[ids.ObjectAddress]*Relation {
	relations := make(map[ids.ObjectAddress]*Relation, len(committed)+staged.DescriptorCount())
	for a, v := range committed {
		relations[a] = v
	}
	for a := range staged.Removals() {
		delete(relations, a)
	}
	for a, d := range staged.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind.Stored() && relations[a] == nil {
			if initial := p.Initial[a]; initial != nil {
				relations[a] = initial
			} else {
				relations[a] = &Relation{}
			}
		}
	}
	for a, shape := range p.Shapes {
		relations[a] = shape.Replacement
	}
	if p.Refresh != nil {
		relations[p.Refreshed] = p.Refresh
	}
	return relations
}

// StampPublishedBirth stamps every row of a privately built store as born at
// the publication's commit frontier by fullXID, unfrozen, and records the
// births in the store's maintenance facts.
func (r *Relation) StampPublishedBirth(frontier ids.CommitSeq, fullXID uint64) {
	for i := range r.Versions {
		r.Versions[i].Born = frontier
		r.Versions[i].InsertFullXID = fullXID
		r.Maintenance.RecordBirth(r.Versions[i].InsertFullXID)
		r.Versions[i].Frozen, r.Versions[i].ImportFrozen = false, false
	}
}

// CountPublishedInserts counts every row of a newly published store as an
// insert and a live row.
func (r *Relation) CountPublishedInserts() {
	r.Maintenance.Inserts += int64(len(r.Versions))
	r.Maintenance.InsertsSince += int64(len(r.Versions))
	r.Maintenance.Modifications += int64(len(r.Versions))
	r.Maintenance.Live += int64(len(r.Versions))
}

// ReplacementCapacityLocked checks that installing relations' rows fits the
// catalog epoch, the row and version identities and the commit sequence.
// The caller holds Engine.mu.
func (s *Store) ReplacementCapacityLocked(relations map[ids.ObjectAddress]*Relation, epoch ids.CatalogEpoch, commit ids.CommitSeq) error {
	if epoch == ids.CatalogEpoch(math.MaxUint64) {
		return fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	var imported uint64
	for _, relation := range relations {
		imported += uint64(len(relation.Versions))
	}
	if imported > ^uint64(0)-s.RowFrontier() {
		return fmt.Errorf("stuntgres: row identity exhausted")
	}
	if imported > ^uint64(0)-s.VersionFrontier() {
		return fmt.Errorf("stuntgres: version identity exhausted")
	}
	if commit == ids.CommitSeq(^uint64(0)) {
		return fmt.Errorf("stuntgres: commit sequence exhausted")
	}
	return nil
}
