package engine

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Inspect logical types, never only their physical codec or simplified result.
func virtualTypeRestriction(t *sqltype.Descriptor) (domain, user bool) {
	if t == nil {
		return false, false
	}
	if t.IsDomain() {
		return true, true
	}
	if t.IsUserDefined() {
		return false, true
	}
	if t.IsArray() {
		return virtualTypeRestriction(t.Element())
	}
	return false, false
}

func virtualDestination(column ColumnDescriptor) error {
	domain, user := virtualTypeRestriction(column.Usage.Type)
	if domain {
		return sqlError("0A000", fmt.Sprintf(`virtual generated column "%s" cannot have a domain type`, column.Name))
	}
	if user {
		err := sqlError("0A000", fmt.Sprintf(`virtual generated column "%s" cannot have a user-defined type`, column.Name)).(*sqlstate.Error)
		err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
		return err
	}
	return nil
}

func virtualGenerationSecurity(node BoundExpr) error {
	var failure error
	walkBoundExpression(node, func(n BoundExpr) {
		if failure != nil {
			return
		}
		if _, user := virtualTypeRestriction(n.ResultUsage().Type); user {
			err := expressionError("0A000", "generation expression uses user-defined type", n.SourcePosition()).(*sqlstate.Error)
			err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
			failure = err
		}
	})
	return failure
}

// The retained plan reads a physical leaf window, preserving the named column's
// ACL identity and the caller's context. Definitions themselves contain plain
// current-row references; no query-specific window enters the catalog.
func (e columnExpression) withGeneration(d *relationDescriptor, physical, start int) columnExpression {
	if d == nil {
		return e
	}
	if definition, ok := d.generations[d.attributes[physical]]; ok && definition.kind == generationVirtual {
		e.virtual = definition.planned
		e.virtualOwner = d
		e.virtualAttribute = d.attributes[physical]
		e.rowStart = start
		e.rowWidth = len(d.columns)
	}
	return e
}

// Generation recipes own their original references, independently of an outer
// query or definer view. Selecting that provenance never changes the invoker.
func selectVirtualGenerationRecipe(c *EvalContext, owner *relationDescriptor, definition columnGeneration) {
	c.routineRecipe, c.routineGraft = true, false
	c.routineChecks = nil
	c.routineReferences = definition.dependencies
	c.routineConsumer = owner
}

func (e columnExpression) admitVirtualGeneration(c *EvalContext, catalog CatalogRead) error {
	if e.virtualOwner == nil {
		return fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	definition, ok := e.virtualOwner.generations[e.virtualAttribute]
	if !ok || definition.kind != generationVirtual || definition.planned == nil {
		return fmt.Errorf("stuntgres: missing original virtual generation")
	}
	recipe := *c
	selectVirtualGenerationRecipe(&recipe, e.virtualOwner, definition)
	recipe.Scope, recipe.Frame = 1, 1
	recipe.Row, recipe.Outer = nil, nil
	node, _, err := preflightCoercionsWithPolicy(&recipe, generationRecipeExpression(definition), true)
	c.preparedDomainChecks = recipe.preparedDomainChecks
	if err != nil {
		return err
	}
	return validateExpressionJoinExecution(&recipe, node, catalog)
}

// CHECK recipes belong to the completed destination, not a copied source.
func refreshGenerationChecks(d *relationDescriptor, catalog CatalogRead) error {
	for i := range d.checkConstraints {
		next, changed, err := mapBoundExpression(catalog, d.checkConstraints[i].expression, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, func(node BoundExpr) (BoundExpr, bool, error) {
			column, ok := node.(columnExpression)
			if !ok || column.origin.Address.ClassID != d.address.ClassID || column.origin.Address.ObjectID != d.address.ObjectID {
				return node, false, nil
			}
			slot := -1
			for i, attribute := range d.attributes {
				if attribute == column.origin.Address.SubID {
					slot = i
					break
				}
			}
			if slot < 0 {
				return nil, false, fmt.Errorf("stuntgres: missing destination CHECK attribute")
			}
			column.slot = slot
			column.virtual = nil
			column.virtualOwner, column.virtualAttribute = nil, 0
			column.rowStart, column.rowWidth, column.queryOccurrence = 0, 0, 0
			column = column.withGeneration(d, slot, 0)
			return column, true, nil
		})
		if err != nil {
			return err
		}
		if changed {
			d.checkConstraints[i].expression = next
		}
	}
	return nil
}

func logicalColumnValue(d *relationDescriptor, slot int, row []sqltype.Value, c *EvalContext) (sqltype.Value, error) {
	if definition, ok := d.generations[d.attributes[slot]]; !ok || definition.kind != generationVirtual {
		return row[slot], nil
	}
	column := columnExpression{expressionInfo: expressionInfo{usage: d.columns[slot].Usage}, scope: 1, frame: 1, slot: slot}.withGeneration(d, slot, 0)
	local := *c
	local.Scope, local.Frame, local.Row = 1, 1, row
	return column.Eval(&local)
}

// Frozen projections do not freeze the queried logical relation's recipe.
// Physical child identity belongs to tableoid/provenance, not recipe selection.
// Rechecks borrow the complete latest logical tuple without rereading a source
// whose inheritance mapping scratch may still hold that same tuple.
func (e columnExpression) capturedGeneration(source *querySource, index int, current []sqltype.Value, c *EvalContext) (BoundExpr, []sqltype.Value, error) {
	data := source.view.data.Load()
	if data == nil {
		return nil, nil, fmt.Errorf("stuntgres: virtual source snapshot is released")
	}
	logical, ok := data.catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing virtual source descriptor")
	}
	definition, ok := logical.generations[e.origin.Address.SubID]
	if !ok || definition.kind != generationVirtual || definition.planned == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing captured virtual generation")
	}
	selectVirtualGenerationRecipe(c, logical, definition)
	if current != nil {
		if len(current) != len(logical.columns) {
			return nil, nil, fmt.Errorf("stuntgres: invalid virtual recheck tuple")
		}
		return definition.planned, current, nil
	}
	return definition.planned, source.row(index), nil
}

func generationInheritanceError(column string, parent, child generationKind) error {
	err := sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column of different kind`, column)).(*sqlstate.Error)
	err.Detail = fmt.Sprintf("Parent column is %s, child column is %s.", parent.sql(), child.sql())
	return err
}

func rejectVirtualIndexUses(parent *relationDescriptor, slots []int) error {
	for _, slot := range slots {
		if definition, ok := parent.generations[parent.attributes[slot]]; ok && definition.kind == generationVirtual {
			return sqlError("0A000", "indexes on virtual generated columns are not supported")
		}
	}
	return nil
}
