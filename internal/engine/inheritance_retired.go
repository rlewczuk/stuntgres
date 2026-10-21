package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func validateRetiredInheritance(view catalog.Read, table *catalog.Relation, p catalog.InheritedConstraint, own ids.OID, check bool) error {
	for i, history := range p.Retired {
		parent, ok := view.Relation(history.Parent.Address)
		if !ok || parent.RelationKind != catalog.TableRelation || !storedReferenceEpochValid(view, table, history.Parent) || history.Parent.Address.SubID != 0 || history.Attribute <= 0 || history.Attribute > 1600 || history.Column == "" || history.Name == "" || history.Constraint == 0 || history.Lifetime == 0 || history.Own != own || history.Check != check || !slices.Contains(table.Inheritance.Parents, parent.Address()) {
			return fmt.Errorf("stuntgres: invalid retired inheritance origin")
		}
		if history.Replayed != (history.Archive != 0) {
			return fmt.Errorf("stuntgres: invalid retired inheritance lifetime namespace")
		}
		if !history.Replayed {
			tombstone, exists := parent.ColumnMeta.Dropped[history.Attribute]
			if !exists || !tombstone.Dropped || parent.HasAttribute(history.Attribute) || catalog.DefinitionOIDUsed(parent, history.Constraint) {
				return fmt.Errorf("stuntgres: retired inheritance source was not removed")
			}
		}
		for _, previous := range p.Retired[:i] {
			if previous.Parent == history.Parent && previous.Archive == history.Archive && previous.Lifetime == history.Lifetime && previous.Constraint == history.Constraint {
				return fmt.Errorf("stuntgres: duplicate retired inheritance origin")
			}
		}
		for _, live := range p.Origins {
			if !history.Replayed && history.Lifetime == uint64(view.Epoch()) && live.Parent == history.Parent.Address && live.Constraint == history.Constraint {
				return fmt.Errorf("stuntgres: live retired inheritance origin")
			}
		}
	}
	if p.Count() > 32767 {
		return fmt.Errorf("stuntgres: too many inherited constraint contributions")
	}
	return nil
}

// view is the read the retired constraints are named against: the stage's
// base while staging, the definition base while preparing.
// retiredBase is the read inheritRetiredColumnConstraints names against.
func (state *inheritanceAlterState) retiredBase() catalog.Read {
	if state.stage != nil {
		return state.stage.Base
	}
	return state.definition.base
}

func inheritRetiredColumnConstraints(state *inheritanceAlterPlan, view catalog.Read, child *catalog.Relation, graph *catalog.TableInheritance, attribute int32) {
	for _, parent := range graph.Parents {
		for _, event := range state.events[parent] {
			if event.action.Action != parser.DropColumn {
				continue
			}
			parentRef := view.Owner().Domain.Ref(view.Epoch(), parent)
			for _, source := range event.retiredNotNull {
				for _, own := range child.Constraints.NotNull {
					if own.Attribute != attribute {
						continue
					}
					provenance := graph.NotNull[attribute]
					for _, origin := range provenance.Origins {
						if origin.Parent == parent && origin.Constraint == source.OID {
							record := catalog.RetiredInheritanceOrigin{Parent: parentRef, Attribute: event.attribute, Column: event.action.Column.Text, Name: source.Name, Constraint: source.OID, Lifetime: uint64(view.Epoch()), Own: own.OID}
							provenance.Retired = append(slices.Clone(provenance.Retired), record)
						}
					}
					provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
						return origin.Parent == parent && origin.Constraint == source.OID
					})
					graph.NotNull[attribute] = provenance
				}
			}
			for _, source := range event.retiredChecks {
				provenance, exists := graph.Checks[source.Name]
				if !exists {
					continue
				}
				own := slices.IndexFunc(child.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == source.Name })
				if own < 0 {
					continue
				}
				for _, origin := range provenance.Origins {
					if origin.Parent == parent && origin.Constraint == source.OID {
						record := catalog.RetiredInheritanceOrigin{Parent: parentRef, Attribute: event.attribute, Column: event.action.Column.Text, Name: source.Name, Constraint: source.OID, Lifetime: uint64(view.Epoch()), Own: child.Constraints.Check[own].OID, Check: true}
						provenance.Retired = append(slices.Clone(provenance.Retired), record)
					}
				}
				provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
					return origin.Parent == parent && origin.Constraint == source.OID
				})
				graph.Checks[source.Name] = provenance
			}
		}
	}
}

func (e *Engine) remapRetiredConstraints(next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	// Runtime certificates share the source catalog's namespace. On replacement,
	// archive that namespace once above every already imported namespace.
	var runtimeArchive uint64
	for _, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		for _, p := range table.Inheritance.NotNull {
			for _, history := range p.Retired {
				runtimeArchive = max(runtimeArchive, history.Archive)
			}
		}
		for _, p := range table.Inheritance.Checks {
			for _, history := range p.Retired {
				runtimeArchive = max(runtimeArchive, history.Archive)
			}
		}
	}
	if runtimeArchive == ^uint64(0) {
		return fmt.Errorf("stuntgres: retired inheritance namespace exhausted")
	}
	runtimeArchive++
	for address, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		namespace, ok := next.Namespace(table.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		has := false
		for _, p := range table.Inheritance.NotNull {
			has = has || len(p.Retired) != 0
		}
		for _, p := range table.Inheritance.Checks {
			has = has || len(p.Retired) != 0
		}
		if !has {
			continue
		}
		old, ok := previous[address].(*catalog.Relation)
		if !ok {
			return fmt.Errorf("stuntgres: missing retired constraint owner")
		}
		graph := catalog.CloneInheritance(table.Inheritance)
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
				record.Parent = e.catalog.Domain.Ref(record.Parent.Epoch, record.Parent.Address)
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
			for _, c := range table.Constraints.NotNull {
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
			for _, c := range table.Constraints.Check {
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
		copy := *table
		copy.Inheritance = graph
		next.SetDescriptor(address, &copy)
	}
	return nil
}

// Actual TYPE reconstruction destroys the old constraint tuple. Validate its
// exact own identity before expiring its historical contributions; actual live
// origins and local authority belong to the ordinary reconstruction callbacks.
func retireRebuiltDefinitionHistory(p catalog.InheritedConstraint, before, after ids.OID) (catalog.InheritedConstraint, error) {
	if len(p.Retired) == 0 || before == after {
		return p, nil
	}
	for i := range p.Retired {
		if p.Retired[i].Own != before || before == 0 || after == 0 {
			return p, fmt.Errorf("stuntgres: retired own constraint replacement mismatch")
		}
	}
	// Expire support in the new graph; never mutate old archived records.
	p.Retired = nil
	return p, nil
}

// CHECK's real rebind/assignment has already supplied the new own OID. Merely
// replacing a name or fabricating an identity cannot rescue stale history.
func retireRebuiltCheckHistory(old, next *catalog.Relation) error {
	if old.Inheritance == nil || next.Inheritance == nil {
		return nil
	}
	var graph *catalog.TableInheritance
	for _, c := range next.Constraints.Check {
		previous := slices.IndexFunc(old.Constraints.Check, func(prior catalog.CheckConstraint) bool { return prior.Name == c.Name })
		if previous < 0 {
			continue
		}
		p := next.Inheritance.Checks[c.Name]
		if len(p.Retired) == 0 || old.Constraints.Check[previous].OID == c.OID {
			continue
		}
		before := old.Constraints.Check[previous]
		if old.Address() != next.Address() || before.NoInherit != c.NoInherit || before.NotValid != c.NotValid {
			return fmt.Errorf("stuntgres: changed retired check reconstruction source")
		}
		replacement, err := retireRebuiltDefinitionHistory(p, before.OID, c.OID)
		if err != nil {
			return err
		}
		if graph == nil {
			graph = catalog.CloneInheritance(next.Inheritance)
		}
		graph.Checks[c.Name] = replacement
	}
	if graph != nil {
		next.Inheritance = graph
	}
	return nil
}

// Called only after the incumbent NN TYPE rebuilder allocates its real new
// identity. Re-addition creates a local definition, not a continuation of the
// destroyed tuple's historical contributions. Actual live origins remain.
func retireRebuiltNotNullHistory(old, next *catalog.Relation) error {
	if old.Inheritance == nil || next.Inheritance == nil {
		return nil
	}
	var graph *catalog.TableInheritance
	for _, fact := range next.Constraints.NotNull {
		provenance := next.Inheritance.NotNull[fact.Attribute]
		if len(provenance.Retired) == 0 {
			continue
		}
		index := slices.IndexFunc(old.Constraints.NotNull, func(before catalog.NotNullConstraint) bool { return before.Attribute == fact.Attribute })
		if index < 0 {
			return fmt.Errorf("stuntgres: missing retired not-null reconstruction source")
		}
		before := old.Constraints.NotNull[index]
		if before.OID == fact.OID {
			continue
		}
		expected := before
		expected.OID = fact.OID
		if expected != fact || old.Address() != next.Address() || old.Inheritance.NotNull[fact.Attribute].Local != provenance.Local {
			return fmt.Errorf("stuntgres: changed retired not-null reconstruction source")
		}
		replacement, err := retireRebuiltDefinitionHistory(provenance, before.OID, fact.OID)
		if err != nil {
			return err
		}
		if graph == nil {
			graph = catalog.CloneInheritance(next.Inheritance)
		}
		graph.NotNull[fact.Attribute] = replacement
	}
	if graph != nil {
		next.Inheritance = graph
	}
	return nil
}

// Released TYPE cleanup internally drops an affected nonlocal definition.
// Without a live parent fact there is no recursive re-add. Historical count
// certifies past support; it is not authority to reconstruct a new local fact.
func releaseRetiredTypeConstraints(old *catalog.Relation, actions []parser.AlterTableAction) *catalog.Relation {
	if old.Inheritance == nil {
		return old
	}
	if !slices.ContainsFunc(actions, func(action parser.AlterTableAction) bool { return action.Action == parser.AlterColumnType }) {
		return old
	}
	affected := func(attribute int32) bool {
		for _, action := range actions {
			if action.Action == parser.AlterColumnType {
				if slot, exists := old.Index[action.Column.Text]; exists && old.Attributes[slot] == attribute {
					return true
				}
			}
		}
		return false
	}
	historicalOnly := func(p catalog.InheritedConstraint) bool {
		return !p.Local && len(p.Origins) == 0 && len(p.Retired) != 0
	}
	var next *catalog.Relation
	copiedNN, copiedCheck := false, false
	copiedPhysical := false
	detach := func() {
		if next != nil {
			return
		}
		copy := *old
		copy.Inheritance = catalog.CloneInheritance(old.Inheritance)
		next = &copy
	}
	for _, fact := range old.Constraints.NotNull {
		if affected(fact.Attribute) && historicalOnly(old.Inheritance.NotNull[fact.Attribute]) {
			detach()
			if !copiedNN {
				next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
				next.Columns = slices.Clone(old.Columns)
				copiedNN = true
			}
			removeDetachedNotNullMetadata(next, fact)
			if proof, retained := physicalNotNullFromType(old, next, fact); retained {
				if !copiedPhysical {
					next.Constraints.PhysicalNotNull = maps.Clone(old.Constraints.PhysicalNotNull)
					if next.Constraints.PhysicalNotNull == nil {
						next.Constraints.PhysicalNotNull = make(map[int32]catalog.PhysicalNotNullProof)
					}
					copiedPhysical = true
				}
				next.Constraints.PhysicalNotNull[fact.Attribute] = proof
			} else {
				next.Columns[next.IndexForAttribute(fact.Attribute)].Nullable = true
			}
		}
	}
	for _, fact := range old.Constraints.Check {
		if !historicalOnly(old.Inheritance.Checks[fact.Name]) {
			continue
		}
		touches := false
		for _, dependency := range fact.Dependencies {
			if dependency.Ref.Address.ClassID == ids.RelationClassID && dependency.Ref.Address.ObjectID == old.Address().ObjectID && affected(dependency.Ref.Address.SubID) {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		detach()
		if !copiedCheck {
			next.Constraints.Check = slices.Clone(old.Constraints.Check)
			copiedCheck = true
		}
		next.Constraints.Check = slices.DeleteFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.OID == fact.OID })
		delete(next.Inheritance.Checks, fact.Name)
	}
	if next == nil {
		return old
	}
	return next
}
