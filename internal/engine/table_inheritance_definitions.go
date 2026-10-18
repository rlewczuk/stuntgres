package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) stageInheritance(op *Operation, auth AuthorizationView, stage *CatalogStage, d *relationDescriptor, b binding) error {
	if len(b.createInheritance) == 0 {
		return nil
	}
	graph := &tableInheritance{Columns: make(map[int32]inheritedColumn, len(d.Columns)), Checks: make(map[string]inheritedConstraint), NotNull: make(map[int32]inheritedConstraint)}
	d.Inheritance = graph
	for slot, c := range d.Columns {
		declaration := b.createDeclarations[slot]
		graph.Columns[d.Attributes[slot]] = inheritedColumn{Local: b.createLocalColumns[c.Name], ExplicitDefault: declaration.defaultExpr != nil || declaration.serial || declaration.copiedDefault, ExplicitGeneration: declaration.generation != nil || declaration.copiedGeneration}
	}
	for _, c := range d.NotNullConstraints {
		graph.NotNull[c.Attribute] = inheritedConstraint{Local: true}
	}
	var defaultConflicts, generationConflicts map[int32]bool
	for _, in := range b.createInheritance {
		graph.Parents = append(graph.Parents, in.source.Address())
		stage.captureLikeSource(in.source)
		mapper := createLikeInput{ref: in.ref, source: in.source, targetSlots: make([]int, len(in.source.Columns))}
		for slot, c := range in.source.Columns {
			targetSlot := d.Index[c.Name]
			mapper.targetSlots[slot] = targetSlot
			target := d.Attributes[targetSlot]
			provenance := graph.Columns[target]
			provenance.Origins = append(provenance.Origins, inheritanceOrigin{Parent: in.source.Address(), Attribute: in.source.Attributes[slot]})
			graph.Columns[target] = provenance
		}
		for _, c := range in.source.NotNullConstraints {
			if c.NoInherit {
				continue
			}
			sourceSlot := slices.Index(in.source.Attributes, c.Attribute)
			targetSlot := mapper.targetSlots[sourceSlot]
			target := d.Attributes[targetSlot]
			provenance := graph.NotNull[target]
			provenance.Origins = append(provenance.Origins, inheritanceOrigin{Parent: in.source.Address(), Attribute: c.Attribute, Constraint: c.OID})
			graph.NotNull[target] = provenance
			if !slices.ContainsFunc(d.NotNullConstraints, func(n notNullConstraint) bool { return n.Attribute == target }) {
				d.NotNullConstraints = append(d.NotNullConstraints, notNullConstraint{Name: c.Name, Attribute: target})
			}
			d.Columns[targetSlot].Nullable = false
		}
		for sourceSlot, sourceAttribute := range in.source.Attributes {
			target := d.Attributes[mapper.targetSlots[sourceSlot]]
			// LIKE expressions arrive after inherited conflicts are checked; only a
			// raw child DEFAULT (including serial) can resolve a parent conflict.
			declaration := b.createDeclarations[mapper.targetSlots[sourceSlot]]
			if old, ok := in.source.Defaults[sourceAttribute]; ok && declaration.defaultExpr == nil && !declaration.serial {
				if incumbent, ok := d.Defaults[target]; ok && !sameGroupExpression(incumbent.Expression, old.Expression) {
					if defaultConflicts == nil {
						defaultConflicts = make(map[int32]bool)
					}
					defaultConflicts[target] = true
				}
				copied := old
				copied.OID = 0
				if err := validateCookedDefaultTemporary(copied.Expression, auth.catalog.MustNamespace(d.Namespace()).Temporary); err != nil {
					return err
				}
				if d.Defaults == nil {
					d.Defaults = make(map[int32]columnDefault)
				}
				d.Defaults[target] = copied
			}
			if old, ok := in.source.Generations[sourceAttribute]; ok && declaration.generation == nil {
				node, err := mapper.remap(old.Expression, d, auth.catalog)
				if err != nil {
					return err
				}
				planned, err := mapper.remap(old.Planned, d, auth.catalog)
				if err != nil {
					return err
				}
				if incumbent, ok := d.Generations[target]; ok && !sameGroupExpression(incumbent.Expression, node) {
					if generationConflicts == nil {
						generationConflicts = make(map[int32]bool)
					}
					generationConflicts[target] = true
				}
				copied := columnGeneration{Kind: old.Kind, Expression: node, Planned: planned}
				if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: d, dependencies: &copied.Dependencies, commentSnapshot: &copied.CatalogSnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				if d.Generations == nil {
					d.Generations = make(map[int32]columnGeneration)
				}
				d.Generations[target] = copied
			}
		}
		for _, old := range orderedChecks(in.source) {
			if old.NoInherit {
				continue
			}
			node, err := mapper.remap(old.Expression, d, auth.catalog)
			if err != nil {
				return err
			}
			provenance := graph.Checks[old.Name]
			provenance.Origins = append(provenance.Origins, inheritanceOrigin{Parent: in.source.Address(), Constraint: old.OID})
			graph.Checks[old.Name] = provenance
			incumbent := slices.IndexFunc(d.CheckConstraints, func(c checkConstraint) bool { return c.Name == old.Name })
			if incumbent >= 0 {
				if !sameGroupExpression(d.CheckConstraints[incumbent].Expression, node) {
					return sqlError("42710", fmt.Sprintf(`check constraint name "%s" appears multiple times but with different expressions`, old.Name))
				}
				continue
			}
			copied := checkConstraint{Name: old.Name, Ordinal: len(d.CheckConstraints), Expression: node}
			var dependencies []ids.ObjectRef
			if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: d, dependencies: &dependencies, commentSnapshot: &copied.CommentSnapshot, visibilitySnapshot: &copied.VisibilitySnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := d.Revision()
				if provider, ok := auth.catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				copied.Dependencies = append(copied.Dependencies, expressionDependency{Ref: ref, Revision: revision})
			}
			d.CheckConstraints = append(d.CheckConstraints, copied)
		}
	}
	for slot, attribute := range d.Attributes {
		if defaultConflicts[attribute] {
			diagnostic := sqlError("42611", fmt.Sprintf(`column "%s" inherits conflicting default values`, d.Columns[slot].Name)).(*sqlstate.Error)
			diagnostic.Hint = "To resolve the conflict, specify a default explicitly."
			return diagnostic
		}
		if generationConflicts[attribute] {
			diagnostic := sqlError("42611", fmt.Sprintf(`column "%s" inherits conflicting generation expressions`, d.Columns[slot].Name)).(*sqlstate.Error)
			diagnostic.Hint = "To resolve the conflict, specify a generation expression explicitly."
			return diagnostic
		}
	}
	used := make(map[string]bool)
	for _, constraint := range d.CheckConstraints {
		used[constraint.Name] = true
	}
	for _, constraint := range d.NotNullConstraints {
		if graph.NotNull[constraint.Attribute].Local && constraint.Name != "" {
			used[constraint.Name] = true
		}
	}
	for i := range d.NotNullConstraints {
		constraint := &d.NotNullConstraints[i]
		if graph.NotNull[constraint.Attribute].Local {
			continue
		}
		if used[constraint.Name] {
			constraint.Name = ""
		} else {
			used[constraint.Name] = true
		}
	}
	slices.SortFunc(d.CheckConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
