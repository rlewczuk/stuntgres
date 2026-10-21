package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) retiredArchiveHighWater() uint64 {
	view := e.CatalogRead()
	var maximum uint64
	for _, descriptor := range view.Descriptors() {
		if table, ok := descriptor.(*catalog.Relation); ok && table.Inheritance != nil {
			for _, p := range table.Inheritance.NotNull {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
			for _, p := range table.Inheritance.Checks {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
		}
	}
	return maximum
}

func (e *Engine) captureHistoricalFacts(refs map[ids.ObjectAddress]ids.ObjectRef, facts map[ids.OID]restoredHistoricalFact) {
	view := e.CatalogRead()
	for _, ref := range refs {
		CaptureHistoricalRelation(view, ref, facts)
	}
}

func (e *Engine) restoreRetiredConstraint(op *session.Operation, raw *parser.Update, authority *restoredHistoryAuthority) error {
	repair, err := RestoreRetiredSyntax(raw)
	if err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", RestoreRetiredBoundary) }
	if authority == nil || authority.Facts == nil || authority.Archives == nil || authority.Sources == nil {
		return reject()
	}
	facts, archives, sources := authority.Facts, authority.Archives, authority.Sources
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil || !actor.Superuser {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.Relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok || old.Inheritance == nil {
		e.mu.Unlock()
		return reject()
	}
	namespace, ok := auth.Catalog.Namespace(old.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		e.mu.Unlock()
		return reject()
	}
	var fact restoredHistoricalFact
	var provenance catalog.InheritedConstraint
	if repair.Check {
		for _, c := range old.Constraints.Check {
			if c.Name == repair.Name {
				fact = restoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Check: true, Attributes: ConstraintAttributeKeys(old, c.Expression), Expression: c.Expression, NotValid: c.NotValid, NoInherit: c.NoInherit}
				provenance = old.Inheritance.Checks[c.Name]
				break
			}
		}
	} else {
		for _, c := range old.Constraints.NotNull {
			if c.Name == repair.Name {
				fact = restoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Attributes: []int32{c.Attribute}, NotValid: c.NotValid, NoInherit: c.NoInherit}
				provenance = old.Inheritance.NotNull[c.Attribute]
				break
			}
		}
	}
	captured, exists := facts[fact.OID]
	if !exists || captured.OID != fact.OID || fact.OID == 0 || captured.Ref != ref || captured.Name != fact.Name || captured.Check != fact.Check || captured.NotValid != fact.NotValid || captured.NoInherit != fact.NoInherit || !slices.Equal(captured.Attributes, fact.Attributes) || !analyze.SameGroupExpression(captured.Expression, fact.Expression) || captured.Local != repair.BeforeLocal || captured.Count != repair.Before || provenance.Local != repair.BeforeLocal || provenance.Count() != repair.Before || len(provenance.Retired) != 0 || len(fact.Attributes) != len(repair.Columns) {
		e.mu.Unlock()
		return reject()
	}
	for i, attribute := range fact.Attributes {
		slot := old.IndexForAttribute(attribute)
		if slot < 0 || old.Columns[slot].Name != repair.Columns[i] {
			e.mu.Unlock()
			return reject()
		}
	}
	var records []catalog.RetiredInheritanceOrigin
	nextArchives := maps.Clone(archives)
	nextSources := maps.Clone(sources)
	nextArchive := authority.NextArchive
	for _, row := range repair.Rows {
		parent, err := e.resolveRelationReference(auth, row.Parent, false)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if !slices.Contains(old.Inheritance.Parents, parent.Address) {
			e.mu.Unlock()
			return reject()
		}
		archive, known := nextArchives[row.Archive]
		if !known {
			if nextArchive == ^uint64(0) {
				e.mu.Unlock()
				return reject()
			}
			nextArchive++
			archive = nextArchive
			nextArchives[row.Archive] = archive
		}
		record := catalog.RetiredInheritanceOrigin{Parent: parent, Attribute: row.Attribute, Column: row.Column, Name: row.Name, Constraint: row.Constraint, Lifetime: row.Lifetime, Archive: archive, Own: fact.OID, Check: fact.Check, Replayed: true}
		identity := retiredSourceIdentity{Parent: parent, Archive: archive, Lifetime: row.Lifetime, Constraint: row.Constraint}
		description := retiredSourceDescription{Column: row.Column, Name: row.Name, Attribute: row.Attribute, Check: row.Check}
		if previous, known := nextSources[identity]; known && previous != description {
			e.mu.Unlock()
			return reject()
		}
		nextSources[identity] = description
		for _, previous := range records {
			if previous.Parent == record.Parent && previous.Archive == record.Archive && previous.Lifetime == record.Lifetime && previous.Constraint == record.Constraint {
				e.mu.Unlock()
				return reject()
			}
		}
		records = append(records, record)
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
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
	staged := e.BeginCatalogStage(auth.Catalog)
	staged.authorizeLocked(auth, op.Session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(staged)
	if err := staged.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), staged)
	if err != nil {
		return err
	}
	if _, err := e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return err
	}
	for source, target := range nextArchives {
		archives[source] = target
	}
	for identity, description := range nextSources {
		sources[identity] = description
	}
	authority.NextArchive = nextArchive
	delete(facts, fact.OID)
	return nil
}
