package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindCycleMarks(raw *parser.CycleClause) (*plan.CycleRecipe, error) {
	value, fallback := raw.Value, raw.Default
	if value == nil {
		value = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: raw.Pos}}
		fallback = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Pos: raw.Pos}}
	}
	operands := make([]comparisonOperand, 2)
	var typedConstants [2]sqltype.Usage
	var err error
	for i, raw := range []parser.Expr{value, fallback} {
		operands[i], err = b.comparisonOperand(raw)
		if err != nil {
			return nil, err
		}
		if _, typed := raw.(parser.TypedLiteralExpr); typed {
			typedConstants[i] = operands[i].Node.ResultUsage()
		}
	}
	common, err := selectCommonUsage(2, func(i int) comparisonOperand { return operands[i] }, b.Type(sqltype.Text).Usage(), "CYCLE", b.recursiveTypeName)
	if err != nil {
		return nil, err
	}
	mark, err := b.CoerceConditional(operands[0], common, "CYCLE/SET/TO")
	if err != nil {
		return nil, err
	}
	otherwise, err := b.CoerceConditional(operands[1], common, "CYCLE/SET/DEFAULT")
	if err != nil {
		return nil, err
	}
	info, err := b.CollatedInfo(b.Info(common, raw.Pos, expr.Properties{}), mark, otherwise)
	if err != nil {
		return nil, err
	}
	nodes := []expr.Expr{mark, otherwise}
	for i, node := range nodes {
		if node.ResultUsage() != info.Usage {
			coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			coercionInfo := info
			coercionInfo.Pos = node.SourcePosition()
			coercionInfo.Property = analyze.CoercionProperties(node, coercionPlan)
			nodes[i] = plan.CoercionExpr{Info: coercionInfo, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
	}
	mark, otherwise = nodes[0], nodes[1]
	if _, err := sqltype.BindGrouping(info.Usage); err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, common.Type, common.Type, b.Type(sqltype.Bool), info.Usage.Collation, info.CollationConflict)
	if err != nil {
		return nil, err
	}
	return &plan.CycleRecipe{Syntax: raw, Mark: mark, Fallback: otherwise, TypedConstants: typedConstants, Inequality: predicate}, nil
}
func (b *expressionBinder) recursiveTypeName(t *sqltype.Descriptor) string {
	return b.recursiveUsageName(t.Usage())
}

func (b *expressionBinder) bindCycleShape(d *plan.CTEDefinition) error {
	c := d.Cycle
	c.MarkSlot = len(d.Schema)
	c.Keys = make([]int, len(c.Syntax.Keys))
	c.Grouping = make([]sqltype.Grouping, len(c.Keys))
	members := make([]sqltype.CompositeMember, len(c.Keys))
	for i, key := range c.Syntax.Keys {
		slot := -1
		for j, column := range d.Schema[:d.UserWidth] {
			if column.Name == key.Text {
				slot = j
				break
			}
		}
		if slot < 0 && c.ShapeError == nil {
			c.ShapeError = expr.ErrorAt("42601", fmt.Sprintf(`cycle column "%s" not in WITH query column list`, key.Text), c.Syntax.Pos)
		}
		for _, prior := range c.Keys[:i] {
			if slot >= 0 && prior == slot && c.ShapeError == nil {
				c.ShapeError = expr.ErrorAt("42701", fmt.Sprintf(`cycle column "%s" specified more than once`, key.Text), c.Syntax.Pos)
			}
		}
		c.Keys[i] = slot
		usage := b.Type(sqltype.Text).Usage()
		if slot >= 0 {
			usage = d.Schema[slot].Usage
		}
		var err error
		c.Grouping[i], err = sqltype.BindGrouping(usage)
		if err != nil && c.ShapeError == nil {
			c.ShapeError = err
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: key.Text, Usage: usage}
	}
	var err error
	c.Record, err = sqltype.RecordShape(members)
	if err != nil {
		return err
	}
	c.Path, err = sqltype.Array(2287, "_record", c.Record)
	if err != nil {
		return err
	}
	d.Schema = append(d.Schema, catalog.Column{Name: c.Syntax.Mark.Text, Usage: c.Mark.ResultUsage()}, catalog.Column{Name: c.Syntax.Path.Text, Usage: c.Path.Usage()})
	return nil
}
func cycleNameError(d *plan.CTEDefinition) error {
	c := d.Cycle
	for _, column := range d.Schema[:d.UserWidth] {
		if column.Name == c.Syntax.Mark.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`cycle mark column name "%s" already used in WITH query column list`, column.Name), c.Syntax.Pos)
		}
	}
	for _, column := range d.Schema[:d.UserWidth] {
		if column.Name == c.Syntax.Path.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`cycle path column name "%s" already used in WITH query column list`, column.Name), c.Syntax.Pos)
		}
	}
	if c.Syntax.Mark.Text == c.Syntax.Path.Text {
		return expr.ErrorAt("42601", "cycle mark column name and cycle path column name are the same", c.Syntax.Pos)
	}
	return nil
}
