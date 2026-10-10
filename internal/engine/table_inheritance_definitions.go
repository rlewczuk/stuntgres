package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) stageInheritance(op *Operation, auth AuthorizationView, stage *CatalogStage, d *relationDescriptor, b binding) error {
	if len(b.createInheritance) == 0 {
		return nil
	}
	graph := &tableInheritance{columns: make(map[int32]inheritedColumn, len(d.columns)), checks: make(map[string]inheritedConstraint), notNull: make(map[int32]inheritedConstraint)}
	d.inheritance = graph
	for slot, c := range d.columns {
		declaration := b.createDeclarations[slot]
		graph.columns[d.attributes[slot]] = inheritedColumn{local: b.createLocalColumns[c.Name], explicitDefault: declaration.defaultExpr != nil || declaration.serial || declaration.copiedDefault, explicitGeneration: declaration.generation != nil || declaration.copiedGeneration}
	}
	for _, c := range d.notNullConstraints {
		graph.notNull[c.Attribute] = inheritedConstraint{local: true}
	}
	var defaultConflicts, generationConflicts map[int32]bool
	for _, in := range b.createInheritance {
		graph.parents = append(graph.parents, in.source.address)
		stage.captureLikeSource(in.source)
		mapper := createLikeInput{ref: in.ref, source: in.source, targetSlots: make([]int, len(in.source.columns))}
		for slot, c := range in.source.columns {
			targetSlot := d.index[c.Name]
			mapper.targetSlots[slot] = targetSlot
			target := d.attributes[targetSlot]
			provenance := graph.columns[target]
			provenance.origins = append(provenance.origins, inheritanceOrigin{parent: in.source.address, attribute: in.source.attributes[slot]})
			graph.columns[target] = provenance
		}
		for _, c := range in.source.notNullConstraints {
			sourceSlot := slices.Index(in.source.attributes, c.Attribute)
			targetSlot := mapper.targetSlots[sourceSlot]
			target := d.attributes[targetSlot]
			provenance := graph.notNull[target]
			provenance.origins = append(provenance.origins, inheritanceOrigin{parent: in.source.address, attribute: c.Attribute, constraint: c.OID})
			graph.notNull[target] = provenance
			if !slices.ContainsFunc(d.notNullConstraints, func(n notNullConstraint) bool { return n.Attribute == target }) {
				d.notNullConstraints = append(d.notNullConstraints, notNullConstraint{Name: c.Name, Attribute: target})
			}
			d.columns[targetSlot].Nullable = false
		}
		for sourceSlot, sourceAttribute := range in.source.attributes {
			target := d.attributes[mapper.targetSlots[sourceSlot]]
			// LIKE expressions arrive after inherited conflicts are checked; only a
			// raw child DEFAULT (including serial) can resolve a parent conflict.
			declaration := b.createDeclarations[mapper.targetSlots[sourceSlot]]
			if old, ok := in.source.defaults[sourceAttribute]; ok && declaration.defaultExpr == nil && !declaration.serial {
				if incumbent, ok := d.defaults[target]; ok && !sameGroupExpression(incumbent.expression, old.expression) {
					if defaultConflicts == nil {
						defaultConflicts = make(map[int32]bool)
					}
					defaultConflicts[target] = true
				}
				copied := old
				copied.OID = 0
				if err := validateCookedDefaultTemporary(copied.expression, auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor).temporary); err != nil {
					return err
				}
				if d.defaults == nil {
					d.defaults = make(map[int32]columnDefault)
				}
				d.defaults[target] = copied
			}
			if old, ok := in.source.generations[sourceAttribute]; ok && declaration.generation == nil {
				node, err := mapper.remap(old.expression, d, auth.catalog)
				if err != nil {
					return err
				}
				planned, err := mapper.remap(old.planned, d, auth.catalog)
				if err != nil {
					return err
				}
				if incumbent, ok := d.generations[target]; ok && !sameGroupExpression(incumbent.expression, node) {
					if generationConflicts == nil {
						generationConflicts = make(map[int32]bool)
					}
					generationConflicts[target] = true
				}
				copied := columnGeneration{expression: node, planned: planned}
				if _, err := (scalarRenderer{owner: d, dependencies: &copied.dependencies, commentSnapshot: &copied.catalogSnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				if d.generations == nil {
					d.generations = make(map[int32]columnGeneration)
				}
				d.generations[target] = copied
			}
		}
		for _, old := range orderedChecks(in.source) {
			if old.NoInherit {
				continue
			}
			node, err := mapper.remap(old.expression, d, auth.catalog)
			if err != nil {
				return err
			}
			provenance := graph.checks[old.Name]
			provenance.origins = append(provenance.origins, inheritanceOrigin{parent: in.source.address, constraint: old.OID})
			graph.checks[old.Name] = provenance
			incumbent := slices.IndexFunc(d.checkConstraints, func(c checkConstraint) bool { return c.Name == old.Name })
			if incumbent >= 0 {
				if !sameGroupExpression(d.checkConstraints[incumbent].expression, node) {
					return sqlError("42710", fmt.Sprintf(`check constraint name "%s" appears multiple times but with different expressions`, old.Name))
				}
				continue
			}
			copied := checkConstraint{Name: old.Name, ordinal: len(d.checkConstraints), expression: node}
			var dependencies []ObjectRef
			if _, err := (scalarRenderer{owner: d, dependencies: &dependencies, commentSnapshot: &copied.commentSnapshot, visibilitySnapshot: &copied.visibilitySnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := d.revision
				if provider, ok := auth.catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				copied.dependencies = append(copied.dependencies, expressionDependency{ref: ref, revision: revision})
			}
			d.checkConstraints = append(d.checkConstraints, copied)
		}
	}
	for slot, attribute := range d.attributes {
		if defaultConflicts[attribute] {
			diagnostic := sqlError("42611", fmt.Sprintf(`column "%s" inherits conflicting default values`, d.columns[slot].Name)).(*sqlstate.Error)
			diagnostic.Hint = "To resolve the conflict, specify a default explicitly."
			return diagnostic
		}
		if generationConflicts[attribute] {
			diagnostic := sqlError("42611", fmt.Sprintf(`column "%s" inherits conflicting generation expressions`, d.columns[slot].Name)).(*sqlstate.Error)
			diagnostic.Hint = "To resolve the conflict, specify a generation expression explicitly."
			return diagnostic
		}
	}
	used := make(map[string]bool)
	for _, constraint := range d.checkConstraints {
		used[constraint.Name] = true
	}
	for _, constraint := range d.notNullConstraints {
		if graph.notNull[constraint.Attribute].local && constraint.Name != "" {
			used[constraint.Name] = true
		}
	}
	for i := range d.notNullConstraints {
		constraint := &d.notNullConstraints[i]
		if graph.notNull[constraint.Attribute].local {
			continue
		}
		if used[constraint.Name] {
			constraint.Name = ""
		} else {
			used[constraint.Name] = true
		}
	}
	slices.SortFunc(d.checkConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
