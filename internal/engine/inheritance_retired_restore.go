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
		captureHistoricalRelation(view, ref, facts)
	}
}

func (e *Engine) restoreRetiredConstraint(op *session.Operation, raw *parser.Update, authority *restoredHistoryAuthority) error {
	repair, err := restoreRetiredSyntax(raw)
	if err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", restoreRetiredBoundary) }
	if authority == nil || authority.facts == nil || authority.archives == nil || authority.sources == nil {
		return reject()
	}
	facts, archives, sources := authority.facts, authority.archives, authority.sources
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
	ref, err := e.resolveRelationReference(auth, repair.relation, false)
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
	if repair.check {
		for _, c := range old.Constraints.Check {
			if c.Name == repair.name {
				fact = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, check: true, attributes: constraintAttributeKeys(old, c.Expression), expression: c.Expression, notValid: c.NotValid, noInherit: c.NoInherit}
				provenance = old.Inheritance.Checks[c.Name]
				break
			}
		}
	} else {
		for _, c := range old.Constraints.NotNull {
			if c.Name == repair.name {
				fact = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, attributes: []int32{c.Attribute}, notValid: c.NotValid, noInherit: c.NoInherit}
				provenance = old.Inheritance.NotNull[c.Attribute]
				break
			}
		}
	}
	captured, exists := facts[fact.oid]
	if !exists || captured.oid != fact.oid || fact.oid == 0 || captured.ref != ref || captured.name != fact.name || captured.check != fact.check || captured.notValid != fact.notValid || captured.noInherit != fact.noInherit || !slices.Equal(captured.attributes, fact.attributes) || !analyze.SameGroupExpression(captured.expression, fact.expression) || captured.local != repair.beforeLocal || captured.count != repair.before || provenance.Local != repair.beforeLocal || provenance.Count() != repair.before || len(provenance.Retired) != 0 || len(fact.attributes) != len(repair.columns) {
		e.mu.Unlock()
		return reject()
	}
	for i, attribute := range fact.attributes {
		slot := old.IndexForAttribute(attribute)
		if slot < 0 || old.Columns[slot].Name != repair.columns[i] {
			e.mu.Unlock()
			return reject()
		}
	}
	var records []catalog.RetiredInheritanceOrigin
	nextArchives := maps.Clone(archives)
	nextSources := maps.Clone(sources)
	nextArchive := authority.nextArchive
	for _, row := range repair.rows {
		parent, err := e.resolveRelationReference(auth, row.parent, false)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if !slices.Contains(old.Inheritance.Parents, parent.Address) {
			e.mu.Unlock()
			return reject()
		}
		archive, known := nextArchives[row.archive]
		if !known {
			if nextArchive == ^uint64(0) {
				e.mu.Unlock()
				return reject()
			}
			nextArchive++
			archive = nextArchive
			nextArchives[row.archive] = archive
		}
		record := catalog.RetiredInheritanceOrigin{Parent: parent, Attribute: row.attribute, Column: row.column, Name: row.name, Constraint: row.constraint, Lifetime: row.lifetime, Archive: archive, Own: fact.oid, Check: fact.check, Replayed: true}
		identity := retiredSourceIdentity{parent: parent, archive: archive, lifetime: row.lifetime, constraint: row.constraint}
		description := retiredSourceDescription{column: row.column, name: row.name, attribute: row.attribute, check: row.check}
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
	provenance.Local = repair.local
	provenance.Retired = records
	if fact.check {
		next.Inheritance.Checks[fact.name] = provenance
	} else {
		next.Inheritance.NotNull[fact.attributes[0]] = provenance
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
	authority.nextArchive = nextArchive
	delete(facts, fact.oid)
	return nil
}
