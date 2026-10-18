package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Retired support is not a live provider edge. PostgreSQL retains its historical
// coninhcount when a parent's column disappears but this physical column stays.
// Source rank/fact identities belong to the archived lifetime, not to compacted
// attribute numbers in a later logical import. The current parent reference and
// this physical constraint's identity remain exact live authority.
type retiredInheritanceOrigin struct {
	parent     ids.ObjectRef
	attribute  int32
	column     string
	name       string
	constraint ids.OID
	lifetime   uint64
	archive    uint64
	own        ids.OID
	check      bool
	// Logical import retains the archived source lifetime, not fresh OID/ranks.
	replayed bool
}

func (p inheritedConstraint) count() int { return len(p.origins) + len(p.retired) }

func validateRetiredInheritance(view CatalogRead, table *relationDescriptor, p inheritedConstraint, own ids.OID, check bool) error {
	for i, history := range p.retired {
		parent, ok := view.state.descriptors[history.parent.Address].(*relationDescriptor)
		if !ok || parent.relationKind != tableRelation || !storedReferenceEpochValid(view, table, history.parent) || history.parent.Address.SubID != 0 || history.attribute <= 0 || history.attribute > 1600 || history.column == "" || history.name == "" || history.constraint == 0 || history.lifetime == 0 || history.own != own || history.check != check || !slices.Contains(table.inheritance.parents, parent.address) {
			return fmt.Errorf("stuntgres: invalid retired inheritance origin")
		}
		if history.replayed != (history.archive != 0) {
			return fmt.Errorf("stuntgres: invalid retired inheritance lifetime namespace")
		}
		if !history.replayed {
			tombstone, exists := parent.droppedAttributes[history.attribute]
			if !exists || !tombstone.dropped || parent.hasAttribute(history.attribute) || definitionOIDUsed(parent, history.constraint) {
				return fmt.Errorf("stuntgres: retired inheritance source was not removed")
			}
		}
		for _, previous := range p.retired[:i] {
			if previous.parent == history.parent && previous.archive == history.archive && previous.lifetime == history.lifetime && previous.constraint == history.constraint {
				return fmt.Errorf("stuntgres: duplicate retired inheritance origin")
			}
		}
		for _, live := range p.origins {
			if !history.replayed && history.lifetime == uint64(view.Epoch()) && live.parent == history.parent.Address && live.constraint == history.constraint {
				return fmt.Errorf("stuntgres: live retired inheritance origin")
			}
		}
	}
	if p.count() > 32767 {
		return fmt.Errorf("stuntgres: too many inherited constraint contributions")
	}
	return nil
}

func inheritRetiredColumnConstraints(state *inheritanceAlterState, child *relationDescriptor, graph *tableInheritance, attribute int32) {
	view := state.definition.base
	if state.stage != nil {
		view = state.stage.base
	}
	for _, parent := range graph.parents {
		for _, event := range state.events[parent] {
			if event.action.Action != parser.DropColumn {
				continue
			}
			parentRef := view.owner.domain.Ref(view.Epoch(), parent)
			for _, source := range event.retiredNotNull {
				for _, own := range child.notNullConstraints {
					if own.Attribute != attribute {
						continue
					}
					provenance := graph.notNull[attribute]
					for _, origin := range provenance.origins {
						if origin.parent == parent && origin.constraint == source.OID {
							record := retiredInheritanceOrigin{parent: parentRef, attribute: event.attribute, column: event.action.Column.Text, name: source.Name, constraint: source.OID, lifetime: uint64(view.Epoch()), own: own.OID}
							provenance.retired = append(slices.Clone(provenance.retired), record)
						}
					}
					provenance.origins = slices.DeleteFunc(slices.Clone(provenance.origins), func(origin inheritanceOrigin) bool { return origin.parent == parent && origin.constraint == source.OID })
					graph.notNull[attribute] = provenance
				}
			}
			for _, source := range event.retiredChecks {
				provenance, exists := graph.checks[source.Name]
				if !exists {
					continue
				}
				own := slices.IndexFunc(child.checkConstraints, func(c checkConstraint) bool { return c.Name == source.Name })
				if own < 0 {
					continue
				}
				for _, origin := range provenance.origins {
					if origin.parent == parent && origin.constraint == source.OID {
						record := retiredInheritanceOrigin{parent: parentRef, attribute: event.attribute, column: event.action.Column.Text, name: source.Name, constraint: source.OID, lifetime: uint64(view.Epoch()), own: child.checkConstraints[own].OID, check: true}
						provenance.retired = append(slices.Clone(provenance.retired), record)
					}
				}
				provenance.origins = slices.DeleteFunc(slices.Clone(provenance.origins), func(origin inheritanceOrigin) bool { return origin.parent == parent && origin.constraint == source.OID })
				graph.checks[source.Name] = provenance
			}
		}
	}
}

func (e *Engine) remapRetiredConstraints(next *catalogState, previous map[ids.ObjectAddress]Descriptor) error {
	// Runtime certificates share the source catalog's namespace. On replacement,
	// archive that namespace once above every already imported namespace.
	var runtimeArchive uint64
	for _, descriptor := range next.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.inheritance == nil {
			continue
		}
		for _, p := range table.inheritance.notNull {
			for _, history := range p.retired {
				runtimeArchive = max(runtimeArchive, history.archive)
			}
		}
		for _, p := range table.inheritance.checks {
			for _, history := range p.retired {
				runtimeArchive = max(runtimeArchive, history.archive)
			}
		}
	}
	if runtimeArchive == ^uint64(0) {
		return fmt.Errorf("stuntgres: retired inheritance namespace exhausted")
	}
	runtimeArchive++
	for address, descriptor := range next.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.inheritance == nil {
			continue
		}
		namespace, ok := next.descriptors[table.namespace].(*namespaceDescriptor)
		if !ok || namespace.temporary != (ids.SessionToken{}) {
			continue
		}
		has := false
		for _, p := range table.inheritance.notNull {
			has = has || len(p.retired) != 0
		}
		for _, p := range table.inheritance.checks {
			has = has || len(p.retired) != 0
		}
		if !has {
			continue
		}
		old, ok := previous[address].(*relationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing retired constraint owner")
		}
		graph := cloneInheritance(table.inheritance)
		remap := func(p inheritedConstraint, oldOID, newOID ids.OID) (inheritedConstraint, error) {
			p.retired = slices.Clone(p.retired)
			for i := range p.retired {
				record := &p.retired[i]
				if record.own != oldOID || oldOID == 0 || newOID == 0 {
					return p, fmt.Errorf("stuntgres: missing retired constraint identity")
				}
				if record.archive == 0 {
					record.archive = runtimeArchive
				}
				record.own = newOID
				record.parent = e.domain.Ref(record.parent.Epoch, record.parent.Address)
				record.parent.Epoch = next.epoch
				record.replayed = true
			}
			return p, nil
		}
		for attribute, p := range graph.notNull {
			if len(p.retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.notNullConstraints {
				if c.Attribute == attribute {
					before = c.OID
				}
			}
			for _, c := range table.notNullConstraints {
				if c.Attribute == attribute {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.notNull[attribute] = replacement
		}
		for name, p := range graph.checks {
			if len(p.retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.checkConstraints {
				if c.Name == name {
					before = c.OID
				}
			}
			for _, c := range table.checkConstraints {
				if c.Name == name {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.checks[name] = replacement
		}
		copy := *table
		copy.inheritance = graph
		next.descriptors[address] = &copy
	}
	return nil
}

// Actual TYPE reconstruction destroys the old constraint tuple. Validate its
// exact own identity before expiring its historical contributions; actual live
// origins and local authority belong to the ordinary reconstruction callbacks.
func retireRebuiltDefinitionHistory(p inheritedConstraint, before, after ids.OID) (inheritedConstraint, error) {
	if len(p.retired) == 0 || before == after {
		return p, nil
	}
	for i := range p.retired {
		if p.retired[i].own != before || before == 0 || after == 0 {
			return p, fmt.Errorf("stuntgres: retired own constraint replacement mismatch")
		}
	}
	// Expire support in the new graph; never mutate old archived records.
	p.retired = nil
	return p, nil
}

// CHECK's real rebind/assignment has already supplied the new own OID. Merely
// replacing a name or fabricating an identity cannot rescue stale history.
func retireRebuiltCheckHistory(old, next *relationDescriptor) error {
	if old.inheritance == nil || next.inheritance == nil {
		return nil
	}
	var graph *tableInheritance
	for _, c := range next.checkConstraints {
		previous := slices.IndexFunc(old.checkConstraints, func(prior checkConstraint) bool { return prior.Name == c.Name })
		if previous < 0 {
			continue
		}
		p := next.inheritance.checks[c.Name]
		if len(p.retired) == 0 || old.checkConstraints[previous].OID == c.OID {
			continue
		}
		before := old.checkConstraints[previous]
		if old.address != next.address || before.NoInherit != c.NoInherit || before.NotValid != c.NotValid {
			return fmt.Errorf("stuntgres: changed retired check reconstruction source")
		}
		replacement, err := retireRebuiltDefinitionHistory(p, before.OID, c.OID)
		if err != nil {
			return err
		}
		if graph == nil {
			graph = cloneInheritance(next.inheritance)
		}
		graph.checks[c.Name] = replacement
	}
	if graph != nil {
		next.inheritance = graph
	}
	return nil
}

// Called only after the incumbent NN TYPE rebuilder allocates its real new
// identity. Re-addition creates a local definition, not a continuation of the
// destroyed tuple's historical contributions. Actual live origins remain.
func retireRebuiltNotNullHistory(old, next *relationDescriptor) error {
	if old.inheritance == nil || next.inheritance == nil {
		return nil
	}
	var graph *tableInheritance
	for _, fact := range next.notNullConstraints {
		provenance := next.inheritance.notNull[fact.Attribute]
		if len(provenance.retired) == 0 {
			continue
		}
		index := slices.IndexFunc(old.notNullConstraints, func(before notNullConstraint) bool { return before.Attribute == fact.Attribute })
		if index < 0 {
			return fmt.Errorf("stuntgres: missing retired not-null reconstruction source")
		}
		before := old.notNullConstraints[index]
		if before.OID == fact.OID {
			continue
		}
		expected := before
		expected.OID = fact.OID
		if expected != fact || old.address != next.address || old.inheritance.notNull[fact.Attribute].local != provenance.local {
			return fmt.Errorf("stuntgres: changed retired not-null reconstruction source")
		}
		replacement, err := retireRebuiltDefinitionHistory(provenance, before.OID, fact.OID)
		if err != nil {
			return err
		}
		if graph == nil {
			graph = cloneInheritance(next.inheritance)
		}
		graph.notNull[fact.Attribute] = replacement
	}
	if graph != nil {
		next.inheritance = graph
	}
	return nil
}

// Released TYPE cleanup internally drops an affected nonlocal definition.
// Without a live parent fact there is no recursive re-add. Historical count
// certifies past support; it is not authority to reconstruct a new local fact.
func releaseRetiredTypeConstraints(old *relationDescriptor, actions []parser.AlterTableAction) *relationDescriptor {
	if old.inheritance == nil {
		return old
	}
	if !slices.ContainsFunc(actions, func(action parser.AlterTableAction) bool { return action.Action == parser.AlterColumnType }) {
		return old
	}
	affected := func(attribute int32) bool {
		for _, action := range actions {
			if action.Action == parser.AlterColumnType {
				if slot, exists := old.index[action.Column.Text]; exists && old.attributes[slot] == attribute {
					return true
				}
			}
		}
		return false
	}
	historicalOnly := func(p inheritedConstraint) bool { return !p.local && len(p.origins) == 0 && len(p.retired) != 0 }
	var next *relationDescriptor
	copiedNN, copiedCheck := false, false
	copiedPhysical := false
	detach := func() {
		if next != nil {
			return
		}
		copy := *old
		copy.inheritance = cloneInheritance(old.inheritance)
		next = &copy
	}
	for _, fact := range old.notNullConstraints {
		if affected(fact.Attribute) && historicalOnly(old.inheritance.notNull[fact.Attribute]) {
			detach()
			if !copiedNN {
				next.notNullConstraints = slices.Clone(old.notNullConstraints)
				next.columns = slices.Clone(old.columns)
				copiedNN = true
			}
			removeDetachedNotNullMetadata(next, fact)
			if proof, retained := physicalNotNullFromType(old, next, fact); retained {
				if !copiedPhysical {
					next.physicalNotNull = maps.Clone(old.physicalNotNull)
					if next.physicalNotNull == nil {
						next.physicalNotNull = make(map[int32]physicalNotNullProof)
					}
					copiedPhysical = true
				}
				next.physicalNotNull[fact.Attribute] = proof
			} else {
				next.columns[next.indexForAttribute(fact.Attribute)].Nullable = true
			}
		}
	}
	for _, fact := range old.checkConstraints {
		if !historicalOnly(old.inheritance.checks[fact.Name]) {
			continue
		}
		touches := false
		for _, dependency := range fact.dependencies {
			if dependency.ref.Address.ClassID == ids.RelationClassID && dependency.ref.Address.ObjectID == old.address.ObjectID && affected(dependency.ref.Address.SubID) {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		detach()
		if !copiedCheck {
			next.checkConstraints = slices.Clone(old.checkConstraints)
			copiedCheck = true
		}
		next.checkConstraints = slices.DeleteFunc(next.checkConstraints, func(c checkConstraint) bool { return c.OID == fact.OID })
		delete(next.inheritance.checks, fact.Name)
	}
	if next == nil {
		return old
	}
	return next
}
