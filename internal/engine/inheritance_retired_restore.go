package engine

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const restoreRetiredBoundary = "restore accepts only paired canonical retired constraint origins"

type restoredHistoricalFact struct {
	ref                 ids.ObjectRef
	oid                 ids.OID
	name                string
	check               bool
	attributes          []int32
	expression          expr.Expr
	notValid, noInherit bool
	local               bool
	count               int
}

type retiredRepairRow struct {
	parent             parser.ObjectName
	text, column, name string
	attribute          int32
	constraint         ids.OID
	lifetime, archive  uint64
	check              bool
}
type retiredRepair struct {
	relation                  parser.ObjectName
	text, name                string
	check, local, beforeLocal bool
	before                    int
	columns                   []string
	rows                      []retiredRepairRow
	parents                   []string
}
type retiredSourceIdentity struct {
	parent            ids.ObjectRef
	archive, lifetime uint64
	constraint        ids.OID
}
type retiredSourceDescription struct {
	column, name string
	attribute    int32
	check        bool
}
type restoredHistoryAuthority struct {
	facts       map[ids.OID]restoredHistoricalFact
	archives    map[uint64]uint64
	sources     map[retiredSourceIdentity]retiredSourceDescription
	nextArchive uint64
}

func (e *Engine) retiredArchiveHighWater() uint64 {
	view := e.CatalogRead()
	var maximum uint64
	for _, descriptor := range view.Descriptors() {
		if table, ok := descriptor.(*relationDescriptor); ok && table.Inheritance != nil {
			for _, p := range table.Inheritance.notNull {
				for _, history := range p.retired {
					maximum = max(maximum, history.archive)
				}
			}
			for _, p := range table.Inheritance.checks {
				for _, history := range p.retired {
					maximum = max(maximum, history.archive)
				}
			}
		}
	}
	return maximum
}

func constraintAttributeKeys(table *relationDescriptor, expression expr.Expr) []int32 {
	var keys []int32
	walkBoundExpression(expression, func(node expr.Expr) {
		if column, ok := node.(columnExpression); ok && column.origin.Address.ClassID == ids.RelationClassID && column.origin.Address.ObjectID == table.address.ObjectID && !slices.Contains(keys, column.origin.Address.SubID) {
			keys = append(keys, column.origin.Address.SubID)
		}
	})
	return keys
}

func captureHistoricalRelation(view CatalogRead, ref ids.ObjectRef, facts map[ids.OID]restoredHistoricalFact) {
	table, ok := view.Relation(ref.Address)
	if !ok || table.Inheritance == nil {
		return
	}
	namespace, ok := view.Namespace(table.namespace)
	if !ok || namespace.temporary != (ids.SessionToken{}) {
		return
	}
	for _, c := range table.NotNullConstraints {
		p := table.Inheritance.notNull[c.Attribute]
		if len(p.retired) != 0 {
			continue
		}
		if old, exists := facts[c.OID]; exists && old.local == p.local && old.count == len(p.origins) && old.notValid == c.NotValid && old.noInherit == c.NoInherit {
			continue
		}
		facts[c.OID] = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, attributes: []int32{c.Attribute}, notValid: c.NotValid, noInherit: c.NoInherit, local: p.local, count: len(p.origins)}
	}
	for _, c := range table.CheckConstraints {
		p := table.Inheritance.checks[c.Name]
		if len(p.retired) != 0 {
			continue
		}
		if old, exists := facts[c.OID]; exists && old.local == p.local && old.count == len(p.origins) && old.notValid == c.NotValid && old.noInherit == c.NoInherit {
			continue
		}
		facts[c.OID] = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, check: true, attributes: constraintAttributeKeys(table, c.expression), expression: c.expression, notValid: c.NotValid, noInherit: c.NoInherit, local: p.local, count: len(p.origins)}
	}
}

func (e *Engine) captureHistoricalFacts(refs map[ids.ObjectAddress]ids.ObjectRef, facts map[ids.OID]restoredHistoricalFact) {
	view := e.CatalogRead()
	for _, ref := range refs {
		captureHistoricalRelation(view, ref, facts)
	}
}

func retiredDeclarationNeeded(view CatalogRead, table *relationDescriptor, p inheritedConstraint, check, notValid bool) bool {
	if p.local {
		return true
	}
	if len(p.retired) == 0 {
		return false
	}
	if notValid {
		return len(p.origins) == 0
	}
	// An invalid parent fact is emitted after data, and cannot provide this
	// captured valid enforcement during CREATE/valid-CHECK reconstruction.
	for _, origin := range p.origins {
		parent, ok := view.Relation(origin.parent)
		if !ok {
			continue
		}
		if check {
			for _, c := range parent.CheckConstraints {
				if c.OID == origin.constraint && !c.NotValid {
					return false
				}
			}
		} else {
			for _, c := range parent.NotNullConstraints {
				if c.OID == origin.constraint && !c.NotValid {
					return false
				}
			}
		}
	}
	return true
}

func dumpNotNullDeclaration(view CatalogRead, table *relationDescriptor, attribute int32) bool {
	if table.Inheritance == nil {
		return true
	}
	for _, c := range table.NotNullConstraints {
		if c.Attribute == attribute {
			return retiredDeclarationNeeded(view, table, table.Inheritance.notNull[attribute], false, c.NotValid)
		}
	}
	return false
}

func dumpCheckDeclaration(view CatalogRead, table *relationDescriptor, c checkConstraint) bool {
	return table.Inheritance == nil || retiredDeclarationNeeded(view, table, table.Inheritance.checks[c.Name], true, c.NotValid)
}

func (t *dumpRelation) emitRetiredConstraints(view CatalogRead, checks bool, emit func(string) error, setOwner func(string) error) error {
	graph := t.descriptor.Inheritance
	if graph == nil {
		return nil
	}
	qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
	emitFact := func(name string, keys []int32, p inheritedConstraint, check, notValid bool) error {
		if len(p.retired) == 0 {
			return nil
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		beforeLocal := retiredDeclarationNeeded(view, t.descriptor, p, check, notValid)
		var rows, parents, keyQueries []string
		for _, attribute := range keys {
			slot := t.descriptor.indexForAttribute(attribute)
			if slot < 0 {
				return fmt.Errorf("stuntgres: missing historical constraint key")
			}
			keyQueries = append(keyQueries, "(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = "+commentLiteral(qualified)+"::regclass AND attname = "+commentLiteral(t.columns[slot].Name)+")")
		}
		kind := "n"
		if check {
			kind = "c"
		}
		for _, history := range p.retired {
			parent, ok := view.Relation(history.parent.Address)
			if !ok {
				return fmt.Errorf("stuntgres: missing retired constraint parent")
			}
			parentName := quoteIdentifier(view.Get(parent.namespace).Name()) + "." + quoteIdentifier(parent.name)
			rows = append(rows, "("+commentLiteral(parentName)+"::regclass, "+commentLiteral(history.column)+", "+strconv.FormatInt(int64(history.attribute), 10)+", "+commentLiteral(kind)+", "+commentLiteral(history.name)+", "+commentLiteral(strconv.FormatUint(history.archive, 10)+":"+strconv.FormatUint(history.lifetime, 10))+", "+commentLiteral(strconv.FormatUint(uint64(history.constraint), 10))+")")
			if !slices.Contains(parents, parentName) {
				parents = append(parents, parentName)
			}
		}
		sql := "UPDATE pg_catalog.pg_constraint SET conislocal = " + strconv.FormatBool(p.local) + ", coninhcount = " + strconv.Itoa(len(p.origins)) + " + (SELECT count(DISTINCT (retired.column1, retired.column6, retired.column7)) FROM (VALUES " + strings.Join(rows, ", ") + ") AS retired) WHERE conrelid = " + commentLiteral(qualified) + "::regclass AND conname = " + commentLiteral(name) + " AND contype = " + commentLiteral(kind) + " AND conkey = ARRAY[" + strings.Join(keyQueries, ", ") + "] AND conislocal = " + strconv.FormatBool(beforeLocal) + " AND coninhcount = " + strconv.Itoa(len(p.origins))
		for _, parent := range parents {
			sql += " AND EXISTS (SELECT 1 FROM pg_catalog.pg_inherits WHERE inhrelid = " + commentLiteral(qualified) + "::regclass AND inhparent = " + commentLiteral(parent) + "::regclass)"
		}
		return emit(sql + ";\n")
	}
	if checks {
		for _, c := range t.descriptor.CheckConstraints {
			if err := emitFact(c.Name, constraintAttributeKeys(t.descriptor, c.expression), graph.checks[c.Name], true, c.NotValid); err != nil {
				return err
			}
		}
	} else {
		for _, c := range t.notNullConstraints {
			if err := emitFact(c.Name, []int32{c.Attribute}, graph.notNull[c.Attribute], false, c.NotValid); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) restoreRetiredConstraint(op *Operation, raw *parser.Update, authority *restoredHistoryAuthority) error {
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
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return reject()
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil || !actor.Superuser {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.catalog.Relation(ref.Address)
	if !ok || old.Inheritance == nil {
		e.mu.Unlock()
		return reject()
	}
	namespace, ok := auth.catalog.Namespace(old.namespace)
	if !ok || namespace.temporary != (ids.SessionToken{}) {
		e.mu.Unlock()
		return reject()
	}
	var fact restoredHistoricalFact
	var provenance inheritedConstraint
	if repair.check {
		for _, c := range old.CheckConstraints {
			if c.Name == repair.name {
				fact = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, check: true, attributes: constraintAttributeKeys(old, c.expression), expression: c.expression, notValid: c.NotValid, noInherit: c.NoInherit}
				provenance = old.Inheritance.checks[c.Name]
				break
			}
		}
	} else {
		for _, c := range old.NotNullConstraints {
			if c.Name == repair.name {
				fact = restoredHistoricalFact{ref: ref, oid: c.OID, name: c.Name, attributes: []int32{c.Attribute}, notValid: c.NotValid, noInherit: c.NoInherit}
				provenance = old.Inheritance.notNull[c.Attribute]
				break
			}
		}
	}
	captured, exists := facts[fact.oid]
	if !exists || captured.oid != fact.oid || fact.oid == 0 || captured.ref != ref || captured.name != fact.name || captured.check != fact.check || captured.notValid != fact.notValid || captured.noInherit != fact.noInherit || !slices.Equal(captured.attributes, fact.attributes) || !sameGroupExpression(captured.expression, fact.expression) || captured.local != repair.beforeLocal || captured.count != repair.before || provenance.local != repair.beforeLocal || provenance.count() != repair.before || len(provenance.retired) != 0 || len(fact.attributes) != len(repair.columns) {
		e.mu.Unlock()
		return reject()
	}
	for i, attribute := range fact.attributes {
		slot := old.indexForAttribute(attribute)
		if slot < 0 || old.Columns[slot].Name != repair.columns[i] {
			e.mu.Unlock()
			return reject()
		}
	}
	var records []retiredInheritanceOrigin
	nextArchives := maps.Clone(archives)
	nextSources := maps.Clone(sources)
	nextArchive := authority.nextArchive
	for _, row := range repair.rows {
		parent, err := e.resolveRelationReference(auth, row.parent, false)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if !slices.Contains(old.Inheritance.parents, parent.Address) {
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
		record := retiredInheritanceOrigin{parent: parent, attribute: row.attribute, column: row.column, name: row.name, constraint: row.constraint, lifetime: row.lifetime, archive: archive, own: fact.oid, check: fact.check, replayed: true}
		identity := retiredSourceIdentity{parent: parent, archive: archive, lifetime: row.lifetime, constraint: row.constraint}
		description := retiredSourceDescription{column: row.column, name: row.name, attribute: row.attribute, check: row.check}
		if previous, known := nextSources[identity]; known && previous != description {
			e.mu.Unlock()
			return reject()
		}
		nextSources[identity] = description
		for _, previous := range records {
			if previous.parent == record.parent && previous.archive == record.archive && previous.lifetime == record.lifetime && previous.constraint == record.constraint {
				e.mu.Unlock()
				return reject()
			}
		}
		records = append(records, record)
	}
	if old.revision == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.revision++
	next.Inheritance = cloneInheritance(old.Inheritance)
	provenance.local = repair.local
	provenance.retired = records
	if fact.check {
		next.Inheritance.checks[fact.name] = provenance
	} else {
		next.Inheritance.notNull[fact.attributes[0]] = provenance
	}
	staged := e.BeginCatalogStage(auth.catalog)
	staged.authorizeLocked(auth, op.session)
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

func retiredConstraintEvents(view CatalogRead, events map[dumpEventKey][]dumpEventKey, tables []dumpRelation) {
	for i := range tables {
		table := tables[i].descriptor
		graph := table.Inheritance
		if graph == nil {
			continue
		}
		has := false
		for _, p := range graph.notNull {
			has = has || len(p.retired) != 0
		}
		for _, p := range graph.checks {
			has = has || len(p.retired) != 0
		}
		if !has {
			continue
		}
		event := dumpEventKey{oid: table.address.ObjectID, phase: dumpRetiredConstraints}
		seen := make(map[ids.ObjectAddress]bool)
		var ready func(ids.ObjectAddress)
		ready = func(address ids.ObjectAddress) {
			if seen[address] {
				return
			}
			seen[address] = true
			addDumpDependency(events, event, dumpEventKey{oid: address.ObjectID, phase: dumpTableCompletion})
			if parent, ok := view.Relation(address); ok && parent.Inheritance != nil {
				for _, ancestor := range parent.Inheritance.parents {
					ready(ancestor)
				}
			}
		}
		ready(table.address)
	}
}
