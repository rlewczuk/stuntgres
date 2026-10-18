package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindCycleMarks(raw *parser.CycleClause) (*cycleRecipe, error) {
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
			typedConstants[i] = operands[i].node.ResultUsage()
		}
	}
	common, err := selectCommonUsage(2, func(i int) comparisonOperand { return operands[i] }, b.typ(sqltype.Text).Usage(), "CYCLE", b.recursiveTypeName)
	if err != nil {
		return nil, err
	}
	mark, err := b.coerceConditional(operands[0], common, "CYCLE/SET/TO")
	if err != nil {
		return nil, err
	}
	otherwise, err := b.coerceConditional(operands[1], common, "CYCLE/SET/DEFAULT")
	if err != nil {
		return nil, err
	}
	info, err := b.collatedInfo(b.info(common, raw.Pos, expr.Properties{}), mark, otherwise)
	if err != nil {
		return nil, err
	}
	nodes := []expr.Expr{mark, otherwise}
	for i, node := range nodes {
		if node.ResultUsage() != info.Usage {
			plan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			coercionInfo := info
			coercionInfo.Pos = node.SourcePosition()
			coercionInfo.Property = coercionProperties(node, plan)
			nodes[i] = coercionExpression{coercionInfo, node, plan, sqltype.Implicit, false}
		}
	}
	mark, otherwise = nodes[0], nodes[1]
	if _, err := sqltype.BindGrouping(info.Usage); err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, common.Type, common.Type, b.typ(sqltype.Bool), info.Usage.Collation, info.CollationConflict)
	if err != nil {
		return nil, err
	}
	return &cycleRecipe{syntax: raw, mark: mark, fallback: otherwise, typedConstants: typedConstants, inequality: predicate}, nil
}
func (b *expressionBinder) recursiveTypeName(t *sqltype.Descriptor) string {
	return b.recursiveUsageName(t.Usage())
}

func (b *expressionBinder) bindCycleShape(d *cteDefinition) error {
	c := d.cycle
	c.markSlot = len(d.schema)
	c.keys = make([]int, len(c.syntax.Keys))
	c.grouping = make([]sqltype.Grouping, len(c.keys))
	members := make([]sqltype.CompositeMember, len(c.keys))
	for i, key := range c.syntax.Keys {
		slot := -1
		for j, column := range d.schema[:d.userWidth] {
			if column.Name == key.Text {
				slot = j
				break
			}
		}
		if slot < 0 && c.shapeError == nil {
			c.shapeError = expr.ErrorAt("42601", fmt.Sprintf(`cycle column "%s" not in WITH query column list`, key.Text), c.syntax.Pos)
		}
		for _, prior := range c.keys[:i] {
			if slot >= 0 && prior == slot && c.shapeError == nil {
				c.shapeError = expr.ErrorAt("42701", fmt.Sprintf(`cycle column "%s" specified more than once`, key.Text), c.syntax.Pos)
			}
		}
		c.keys[i] = slot
		usage := b.typ(sqltype.Text).Usage()
		if slot >= 0 {
			usage = d.schema[slot].Usage
		}
		var err error
		c.grouping[i], err = sqltype.BindGrouping(usage)
		if err != nil && c.shapeError == nil {
			c.shapeError = err
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: key.Text, Usage: usage}
	}
	var err error
	c.record, err = sqltype.RecordShape(members)
	if err != nil {
		return err
	}
	c.path, err = sqltype.Array(2287, "_record", c.record)
	if err != nil {
		return err
	}
	d.schema = append(d.schema, ColumnDescriptor{Name: c.syntax.Mark.Text, Usage: c.mark.ResultUsage()}, ColumnDescriptor{Name: c.syntax.Path.Text, Usage: c.path.Usage()})
	return nil
}
func cycleNameError(d *cteDefinition) error {
	c := d.cycle
	for _, column := range d.schema[:d.userWidth] {
		if column.Name == c.syntax.Mark.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`cycle mark column name "%s" already used in WITH query column list`, column.Name), c.syntax.Pos)
		}
	}
	for _, column := range d.schema[:d.userWidth] {
		if column.Name == c.syntax.Path.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`cycle path column name "%s" already used in WITH query column list`, column.Name), c.syntax.Pos)
		}
	}
	if c.syntax.Mark.Text == c.syntax.Path.Text {
		return expr.ErrorAt("42601", "cycle mark column name and cycle path column name are the same", c.syntax.Pos)
	}
	return nil
}
