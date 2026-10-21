package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) stageInheritance(op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Relation, b analyze.Binding) error {
	if len(b.Create.Inheritance) == 0 {
		return nil
	}
	graph := &catalog.TableInheritance{Columns: make(map[int32]catalog.InheritedColumn, len(d.Columns)), Checks: make(map[string]catalog.InheritedConstraint), NotNull: make(map[int32]catalog.InheritedConstraint)}
	d.Inheritance = graph
	for slot, c := range d.Columns {
		declaration := b.Create.Declarations[slot]
		graph.Columns[d.Attributes[slot]] = catalog.InheritedColumn{Local: b.Create.LocalColumns[c.Name], ExplicitDefault: declaration.DefaultExpr != nil || declaration.Serial || declaration.CopiedDefault, ExplicitGeneration: declaration.Generation != nil || declaration.CopiedGeneration}
	}
	for _, c := range d.Constraints.NotNull {
		graph.NotNull[c.Attribute] = catalog.InheritedConstraint{Local: true}
	}
	var defaultConflicts, generationConflicts map[int32]bool
	for _, in := range b.Create.Inheritance {
		graph.Parents = append(graph.Parents, in.Source.Address())
		stage.captureLikeSource(in.Source)
		mapper := analyze.LikeInput{Ref: in.Ref, Source: in.Source, TargetSlots: make([]int, len(in.Source.Columns))}
		for slot, c := range in.Source.Columns {
			targetSlot := d.Index[c.Name]
			mapper.TargetSlots[slot] = targetSlot
			target := d.Attributes[targetSlot]
			provenance := graph.Columns[target]
			provenance.Origins = append(provenance.Origins, catalog.InheritanceOrigin{Parent: in.Source.Address(), Attribute: in.Source.Attributes[slot]})
			graph.Columns[target] = provenance
		}
		for _, c := range in.Source.Constraints.NotNull {
			if c.NoInherit {
				continue
			}
			sourceSlot := slices.Index(in.Source.Attributes, c.Attribute)
			targetSlot := mapper.TargetSlots[sourceSlot]
			target := d.Attributes[targetSlot]
			provenance := graph.NotNull[target]
			provenance.Origins = append(provenance.Origins, catalog.InheritanceOrigin{Parent: in.Source.Address(), Attribute: c.Attribute, Constraint: c.OID})
			graph.NotNull[target] = provenance
			if !slices.ContainsFunc(d.Constraints.NotNull, func(n catalog.NotNullConstraint) bool { return n.Attribute == target }) {
				d.Constraints.NotNull = append(d.Constraints.NotNull, catalog.NotNullConstraint{Name: c.Name, Attribute: target})
			}
			d.Columns[targetSlot].Nullable = false
		}
		for sourceSlot, sourceAttribute := range in.Source.Attributes {
			target := d.Attributes[mapper.TargetSlots[sourceSlot]]
			// LIKE expressions arrive after inherited conflicts are checked; only a
			// raw child DEFAULT (including serial) can resolve a parent conflict.
			declaration := b.Create.Declarations[mapper.TargetSlots[sourceSlot]]
			if old, ok := in.Source.ColumnMeta.Defaults[sourceAttribute]; ok && declaration.DefaultExpr == nil && !declaration.Serial {
				if incumbent, ok := d.ColumnMeta.Defaults[target]; ok && !analyze.SameGroupExpression(incumbent.Expression, old.Expression) {
					if defaultConflicts == nil {
						defaultConflicts = make(map[int32]bool)
					}
					defaultConflicts[target] = true
				}
				copied := old
				copied.OID = 0
				if err := validateCookedDefaultTemporary(copied.Expression, auth.Catalog.MustNamespace(d.Namespace()).Temporary); err != nil {
					return err
				}
				if d.ColumnMeta.Defaults == nil {
					d.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
				}
				d.ColumnMeta.Defaults[target] = copied
			}
			if old, ok := in.Source.ColumnMeta.Generations[sourceAttribute]; ok && declaration.Generation == nil {
				node, err := remapLikeExpression(&mapper, old.Expression, d, auth.Catalog)
				if err != nil {
					return err
				}
				planned, err := remapLikeExpression(&mapper, old.Planned, d, auth.Catalog)
				if err != nil {
					return err
				}
				if incumbent, ok := d.ColumnMeta.Generations[target]; ok && !analyze.SameGroupExpression(incumbent.Expression, node) {
					if generationConflicts == nil {
						generationConflicts = make(map[int32]bool)
					}
					generationConflicts[target] = true
				}
				copied := catalog.ColumnGeneration{Kind: old.Kind, Expression: node, Planned: planned}
				if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, owner: d, dependencies: &copied.Dependencies, commentSnapshot: &copied.CatalogSnapshot}).sql(op.Context(), auth.Catalog, node); err != nil {
					return err
				}
				if d.ColumnMeta.Generations == nil {
					d.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
				}
				d.ColumnMeta.Generations[target] = copied
			}
		}
		for _, old := range orderedChecks(in.Source) {
			if old.NoInherit {
				continue
			}
			node, err := remapLikeExpression(&mapper, old.Expression, d, auth.Catalog)
			if err != nil {
				return err
			}
			provenance := graph.Checks[old.Name]
			provenance.Origins = append(provenance.Origins, catalog.InheritanceOrigin{Parent: in.Source.Address(), Constraint: old.OID})
			graph.Checks[old.Name] = provenance
			incumbent := slices.IndexFunc(d.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == old.Name })
			if incumbent >= 0 {
				if !analyze.SameGroupExpression(d.Constraints.Check[incumbent].Expression, node) {
					return sqlError("42710", fmt.Sprintf(`check constraint name "%s" appears multiple times but with different expressions`, old.Name))
				}
				continue
			}
			copied := catalog.CheckConstraint{Name: old.Name, Ordinal: len(d.Constraints.Check), Expression: node}
			var dependencies []ids.ObjectRef
			if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, owner: d, dependencies: &dependencies, commentSnapshot: &copied.CommentSnapshot, visibilitySnapshot: &copied.VisibilitySnapshot}).sql(op.Context(), auth.Catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := d.Revision()
				if provider, ok := auth.Catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				copied.Dependencies = append(copied.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
			}
			d.Constraints.Check = append(d.Constraints.Check, copied)
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
	for _, constraint := range d.Constraints.Check {
		used[constraint.Name] = true
	}
	for _, constraint := range d.Constraints.NotNull {
		if graph.NotNull[constraint.Attribute].Local && constraint.Name != "" {
			used[constraint.Name] = true
		}
	}
	for i := range d.Constraints.NotNull {
		constraint := &d.Constraints.NotNull[i]
		if graph.NotNull[constraint.Attribute].Local {
			continue
		}
		if used[constraint.Name] {
			constraint.Name = ""
		} else {
			used[constraint.Name] = true
		}
	}
	slices.SortFunc(d.Constraints.Check, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
