package engine

import (
	"fmt"
	"iter"
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// RestoreResolver resolves the relation names of Restore repairs.
type RestoreResolver interface {
	// ResolveRestoredRelation resolves a relation name of a Restore repair.
	ResolveRestoredRelation(auth ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error)
}

// retiredHighWater is the highest retired-origin archive any relation of
// descriptors records.
func retiredHighWater(descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) uint64 {
	var maximum uint64
	for _, descriptor := range descriptors {
		if tbl, ok := descriptor.(*catalog.Relation); ok && tbl.Inheritance != nil {
			for _, p := range tbl.Inheritance.NotNull {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
			for _, p := range tbl.Inheritance.Checks {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
		}
	}
	return maximum
}

// RetiredArchiveHighWater is the highest retired-origin archive in view;
// Restore numbers imported archives above it.
func RetiredArchiveHighWater(view catalog.Read) uint64 {
	return retiredHighWater(view.Descriptors())
}

// CaptureHistoricalFacts records, in facts, the constraint facts of the
// relations Restore created, for the retired-origin repairs that follow.
func CaptureHistoricalFacts(view catalog.Read, refs map[ids.ObjectAddress]ids.ObjectRef, facts map[ids.OID]table.RestoredHistoricalFact) {
	for _, ref := range refs {
		table.CaptureHistoricalRelation(view, ref, facts)
	}
}

// HistoryAuthorityComplete reports whether authority holds every map a
// retired-origin repair reads.
func HistoryAuthorityComplete(authority *table.RestoredHistoryAuthority) bool {
	return authority != nil && authority.Facts != nil && authority.Archives != nil && authority.Sources != nil
}

// RetiredRepairCommit is what one published retired-origin repair records
// in the Restore history authority.
type RetiredRepairCommit struct {
	fact        ids.OID
	archives    map[uint64]uint64
	sources     map[table.RetiredSourceIdentity]table.RetiredSourceDescription
	nextArchive uint64
}

// CommitRetiredRepair records a published repair: its archive and source
// numbering, and the consumed fact.
func CommitRetiredRepair(authority *table.RestoredHistoryAuthority, c RetiredRepairCommit) {
	for source, target := range c.archives {
		authority.Archives[source] = target
	}
	for identity, description := range c.sources {
		authority.Sources[identity] = description
	}
	authority.NextArchive = c.nextArchive
	delete(authority.Facts, c.fact)
}

// RetiredConstraintCandidate returns ref's relation with the retired
// origins of repair restored on its captured constraint, and the commit to
// apply to authority once that relation is published. The constraint must
// match its captured fact exactly and carry no retired history yet; r
// resolves each row's parent with auth, which must be one of ref's parents.
func RetiredConstraintCandidate(r RestoreResolver, auth ddl.Authority, view catalog.Read, ref ids.ObjectRef, repair table.RetiredRepair, authority *table.RestoredHistoryAuthority) (*catalog.Relation, RetiredRepairCommit, error) {
	reject := func() (*catalog.Relation, RetiredRepairCommit, error) {
		return nil, RetiredRepairCommit{}, sqlError("0A000", table.RestoreRetiredBoundary)
	}
	old, ok := view.Relation(ref.Address)
	if !ok || old.Inheritance == nil {
		return reject()
	}
	namespace, ok := view.Namespace(old.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	var fact table.RestoredHistoricalFact
	var provenance catalog.InheritedConstraint
	if repair.Check {
		for _, c := range old.Constraints.Check {
			if c.Name == repair.Name {
				fact = table.RestoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Check: true, Attributes: table.ConstraintAttributeKeys(old, c.Expression), Expression: c.Expression, NotValid: c.NotValid, NoInherit: c.NoInherit}
				provenance = old.Inheritance.Checks[c.Name]
				break
			}
		}
	} else {
		for _, c := range old.Constraints.NotNull {
			if c.Name == repair.Name {
				fact = table.RestoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Attributes: []int32{c.Attribute}, NotValid: c.NotValid, NoInherit: c.NoInherit}
				provenance = old.Inheritance.NotNull[c.Attribute]
				break
			}
		}
	}
	captured, exists := authority.Facts[fact.OID]
	if !exists || captured.OID != fact.OID || fact.OID == 0 || captured.Ref != ref || captured.Name != fact.Name || captured.Check != fact.Check || captured.NotValid != fact.NotValid || captured.NoInherit != fact.NoInherit || !slices.Equal(captured.Attributes, fact.Attributes) || !analyze.SameGroupExpression(captured.Expression, fact.Expression) || captured.Local != repair.BeforeLocal || captured.Count != repair.Before || provenance.Local != repair.BeforeLocal || provenance.Count() != repair.Before || len(provenance.Retired) != 0 || len(fact.Attributes) != len(repair.Columns) {
		return reject()
	}
	for i, attribute := range fact.Attributes {
		slot := old.IndexForAttribute(attribute)
		if slot < 0 || old.Columns[slot].Name != repair.Columns[i] {
			return reject()
		}
	}
	var records []catalog.RetiredInheritanceOrigin
	commit := RetiredRepairCommit{fact: fact.OID, archives: maps.Clone(authority.Archives), sources: maps.Clone(authority.Sources), nextArchive: authority.NextArchive}
	for _, row := range repair.Rows {
		parent, err := r.ResolveRestoredRelation(auth, row.Parent)
		if err != nil {
			return nil, RetiredRepairCommit{}, err
		}
		if !slices.Contains(old.Inheritance.Parents, parent.Address) {
			return reject()
		}
		archive, known := commit.archives[row.Archive]
		if !known {
			if commit.nextArchive == ^uint64(0) {
				return reject()
			}
			commit.nextArchive++
			archive = commit.nextArchive
			commit.archives[row.Archive] = archive
		}
		record := catalog.RetiredInheritanceOrigin{Parent: parent, Attribute: row.Attribute, Column: row.Column, Name: row.Name, Constraint: row.Constraint, Lifetime: row.Lifetime, Archive: archive, Own: fact.OID, Check: fact.Check, Replayed: true}
		identity := table.RetiredSourceIdentity{Parent: parent, Archive: archive, Lifetime: row.Lifetime, Constraint: row.Constraint}
		description := table.RetiredSourceDescription{Column: row.Column, Name: row.Name, Attribute: row.Attribute, Check: row.Check}
		if previous, known := commit.sources[identity]; known && previous != description {
			return reject()
		}
		commit.sources[identity] = description
		for _, previous := range records {
			if previous.Parent == record.Parent && previous.Archive == record.Archive && previous.Lifetime == record.Lifetime && previous.Constraint == record.Constraint {
				return reject()
			}
		}
		records = append(records, record)
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, RetiredRepairCommit{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Inheritance = catalog.CloneInheritance(old.Inheritance)
	provenance.Local = repair.Local
	provenance.Retired = records
	if fact.Check {
		next.Inheritance.Checks[fact.Name] = provenance
	} else {
		next.Inheritance.NotNull[fact.Attributes[0]] = provenance
	}
	return &next, commit, nil
}

// RemapRetiredConstraints renumbers the retired origins of next, a catalog
// replacing previous: each permanent relation's records follow its
// constraints' new OIDs, take next's epoch and parents under domain, and
// runtime records get one archive above every imported one.
func RemapRetiredConstraints(domain *ids.Domain, next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	// Runtime certificates share the source catalog's namespace. On replacement,
	// archive that namespace once above every already imported namespace.
	runtimeArchive := retiredHighWater(next.Descriptors())
	if runtimeArchive == ^uint64(0) {
		return fmt.Errorf("stuntgres: retired inheritance namespace exhausted")
	}
	runtimeArchive++
	for address, descriptor := range next.Descriptors() {
		tbl, ok := descriptor.(*catalog.Relation)
		if !ok || tbl.Inheritance == nil {
			continue
		}
		namespace, ok := next.Namespace(tbl.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		has := false
		for _, p := range tbl.Inheritance.NotNull {
			has = has || len(p.Retired) != 0
		}
		for _, p := range tbl.Inheritance.Checks {
			has = has || len(p.Retired) != 0
		}
		if !has {
			continue
		}
		old, ok := previous[address].(*catalog.Relation)
		if !ok {
			return fmt.Errorf("stuntgres: missing retired constraint owner")
		}
		graph := catalog.CloneInheritance(tbl.Inheritance)
		remap := func(p catalog.InheritedConstraint, oldOID, newOID ids.OID) (catalog.InheritedConstraint, error) {
			p.Retired = slices.Clone(p.Retired)
			for i := range p.Retired {
				record := &p.Retired[i]
				if record.Own != oldOID || oldOID == 0 || newOID == 0 {
					return p, fmt.Errorf("stuntgres: missing retired constraint identity")
				}
				if record.Archive == 0 {
					record.Archive = runtimeArchive
				}
				record.Own = newOID
				record.Parent = domain.Ref(record.Parent.Epoch, record.Parent.Address)
				record.Parent.Epoch = next.Epoch()
				record.Replayed = true
			}
			return p, nil
		}
		for attribute, p := range graph.NotNull {
			if len(p.Retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.Constraints.NotNull {
				if c.Attribute == attribute {
					before = c.OID
				}
			}
			for _, c := range tbl.Constraints.NotNull {
				if c.Attribute == attribute {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.NotNull[attribute] = replacement
		}
		for name, p := range graph.Checks {
			if len(p.Retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.Constraints.Check {
				if c.Name == name {
					before = c.OID
				}
			}
			for _, c := range tbl.Constraints.Check {
				if c.Name == name {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.Checks[name] = replacement
		}
		copy := *tbl
		copy.Inheritance = graph
		next.SetDescriptor(address, &copy)
	}
	return nil
}
