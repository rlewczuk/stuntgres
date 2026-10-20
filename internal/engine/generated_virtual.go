package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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

func virtualDestination(column catalog.Column) error {
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

func virtualGenerationSecurity(node expr.Expr) error {
	var failure error
	walkBoundExpression(node, func(n expr.Expr) {
		if failure != nil {
			return
		}
		if _, user := virtualTypeRestriction(n.ResultUsage().Type); user {
			err := expr.ErrorAt("0A000", "generation expression uses user-defined type", n.SourcePosition()).(*sqlstate.Error)
			err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
			failure = err
		}
	})
	return failure
}

// The retained plan reads a physical leaf window, preserving the named column's
// ACL identity and the caller's context. Definitions themselves contain plain
// current-row references; no query-specific window enters the catalog.
func (e columnExpression) withGeneration(d *catalog.Relation, physical, start int) columnExpression {
	if d == nil {
		return e
	}
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[physical]]; ok && definition.Kind == catalog.GenerationVirtual {
		e.virtual = definition.Planned
		e.virtualOwner = d
		e.virtualAttribute = d.Attributes[physical]
		e.rowStart = start
		e.rowWidth = len(d.Columns)
	}
	return e
}

// Generation recipes own their original references, independently of an outer
// query or definer view. Selecting that provenance never changes the invoker.
func selectVirtualGenerationRecipe(c *EvalContext, owner *catalog.Relation, definition catalog.ColumnGeneration) {
	c.routineRecipe, c.routineGraft = true, false
	c.routineChecks = nil
	c.routineReferences = definition.Dependencies
	c.routineConsumer = owner
}

func (c *EvalContext) admitColumnVirtualGeneration(e columnExpression, view catalog.Read) error {
	if e.virtualOwner == nil {
		return fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	definition, ok := e.virtualOwner.ColumnMeta.Generations[e.virtualAttribute]
	if !ok || definition.Kind != catalog.GenerationVirtual || definition.Planned == nil {
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
	return validateExpressionJoinExecution(&recipe, node, view)
}

// CHECK recipes belong to the completed destination, not a copied source.
func refreshGenerationChecks(d *catalog.Relation, catalog catalog.Read) error {
	for i := range d.Constraints.Check {
		next, changed, err := mapBoundExpression(catalog, d.Constraints.Check[i].Expression, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) {
			column, ok := node.(columnExpression)
			if !ok || column.origin.Address.ClassID != d.Address().ClassID || column.origin.Address.ObjectID != d.Address().ObjectID {
				return node, false, nil
			}
			slot := -1
			for i, attribute := range d.Attributes {
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
			d.Constraints.Check[i].Expression = next
		}
	}
	return nil
}

func logicalColumnValue(d *catalog.Relation, slot int, row []sqltype.Value, c *EvalContext) (sqltype.Value, error) {
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[slot]]; !ok || definition.Kind != catalog.GenerationVirtual {
		return row[slot], nil
	}
	column := columnExpression{Info: expr.Info{Usage: d.Columns[slot].Usage}, scope: 1, frame: 1, slot: slot}.withGeneration(d, slot, 0)
	local := *c
	local.Scope, local.Frame, local.Row = 1, 1, row
	return column.Eval(&local)
}

// Frozen projections do not freeze the queried logical relation's recipe.
// Physical child identity belongs to tableoid/provenance, not recipe selection.
// Rechecks borrow the complete latest logical tuple without rereading a source
// whose inheritance mapping scratch may still hold that same tuple.
func (c *EvalContext) columnCapturedGeneration(e columnExpression, source *querySource, index int, current []sqltype.Value) (expr.Expr, []sqltype.Value, error) {
	data := source.view.Load()
	if data == nil {
		return nil, nil, fmt.Errorf("stuntgres: virtual source snapshot is released")
	}
	logical, ok := data.Catalog.Relation(source.ref.Address)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing virtual source descriptor")
	}
	definition, ok := logical.ColumnMeta.Generations[e.origin.Address.SubID]
	if !ok || definition.Kind != catalog.GenerationVirtual || definition.Planned == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing captured virtual generation")
	}
	selectVirtualGenerationRecipe(c, logical, definition)
	if current != nil {
		if len(current) != len(logical.Columns) {
			return nil, nil, fmt.Errorf("stuntgres: invalid virtual recheck tuple")
		}
		return definition.Planned, current, nil
	}
	return definition.Planned, source.row(index), nil
}

func generationInheritanceError(column string, parent, child catalog.GenerationKind) error {
	err := sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column of different kind`, column)).(*sqlstate.Error)
	err.Detail = fmt.Sprintf("Parent column is %s, child column is %s.", parent.SQL(), child.SQL())
	return err
}

func rejectVirtualIndexUses(parent *catalog.Relation, slots []int) error {
	for _, slot := range slots {
		if definition, ok := parent.ColumnMeta.Generations[parent.Attributes[slot]]; ok && definition.Kind == catalog.GenerationVirtual {
			return sqlError("0A000", "indexes on virtual generated columns are not supported")
		}
	}
	return nil
}
