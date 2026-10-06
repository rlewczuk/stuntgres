package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// unknownOutput is an occurrence, not a SQL value. Assignment or ordinary
// SELECT output finalization must eliminate it before the query can execute.
type unknownOutput struct {
	expressionInfo
	raw parser.Expr
}

func (unknownOutput) Eval(*EvalContext) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unresolved query output")
}

func (b *expressionBinder) bindOutput(raw parser.Expr, contextual bool) (BoundExpr, error) {
	if contextual {
		if v, ok := raw.(parser.LiteralExpr); ok && (v.Value.Kind == parser.StringValue || v.Value.Kind == parser.NullValue) {
			return unknownOutput{expressionInfo: expressionInfo{pos: raw.SourcePosition()}, raw: raw}, nil
		}
	}
	return b.bind(raw, expressionPolicy{context: projectionContext})
}

// bindSelect is shared by public SELECT and the internal INSERT producer.
// Only the latter defers output unknowns to destination assignment.
func (b *expressionBinder) bindSelect(s *parser.Select, contextual bool) (*BoundQuery, error) {
	width := len(s.Targets)
	if s.Star {
		if b.relation == nil {
			return nil, expressionError("42601", "SELECT * with no tables specified is not valid", s.StarPos)
		}
		width = len(b.relation.columns)
	}
	projection := make([]BoundExpr, width)
	var root LogicalNode = &singletonNode{}
	if b.relation != nil {
		root = &sourceNode{querySchema: b.relation.columns, ref: b.ref}
	}
	for i := range width {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if s.Star {
			projection[i] = b.columnAt(i, s.StarPos)
		} else {
			projection[i], err = b.bindOutput(s.Targets[i], contextual)
		}
		if err != nil {
			return nil, err
		}
	}
	if s.Where != nil {
		filter, err := b.bind(s.Where, expressionPolicy{context: filterContext, expected: b.typ(sqltype.Bool).Usage(), mode: sqltype.Implicit})
		if err != nil {
			return nil, err
		}
		root = &filterNode{querySchema: root.Schema(), child: root, predicate: filter}
	}
	if s.Order != nil {
		expr, err := b.column(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: s.Order.Column, Pos: s.Order.Pos}}}, Pos: s.Order.Pos})
		if err != nil {
			return nil, err
		}
		slot := expr.(columnExpression).slot
		cmp, err := sqltype.BindComparison(b.relation.columns[slot].Usage.Type, b.relation.columns[slot].Usage.Type)
		if err != nil {
			return nil, err
		}
		root = &sortNode{querySchema: root.Schema(), child: root, slot: slot, comparison: cmp, desc: s.Order.Desc}
	}
	results := make([]ColumnDescriptor, width)
	regclassOutput := false
	for i, expr := range projection {
		if !contextual {
			var err error
			expr, err = b.finalizeProjection(expr)
			if err != nil {
				return nil, err
			}
			projection[i] = expr
		}
		result := ColumnDescriptor{Name: "?column?", Usage: expr.ResultUsage()}
		regclassOutput = regclassOutput || (result.Usage.Type != nil && result.Usage.Type.ID() == sqltype.Regclass)
		if !s.Star {
			result.Name, _ = expressionName(s.Targets[i], expr.ResultUsage())
		}
		if c, ok := expr.(columnExpression); ok {
			result = b.relation.columns[c.slot]
			if !s.Star {
				result.Name, _ = expressionName(s.Targets[i], expr.ResultUsage())
			}
		}
		results[i] = result
	}
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}, regclassOutput: regclassOutput}, nil
}

func (b *expressionBinder) assignOutput(expr BoundExpr, target ColumnDescriptor) (BoundExpr, error) {
	if raw, ok := expr.(unknownOutput); ok {
		return b.bind(raw.raw, expressionPolicy{context: assignmentContext, expected: target.Usage, mode: sqltype.Assignment})
	}
	if p, ok := expr.(parameterExpression); ok && p.usage.Type == nil {
		var err error
		expr, err = b.coerceParameter(p, target.Usage)
		if err != nil {
			return nil, err
		}
	}
	if expr.ResultUsage() == target.Usage {
		return expr, nil
	}
	plan, err := sqltype.PlanCoercion(expr.ResultUsage(), target.Usage, sqltype.Assignment)
	if err != nil {
		d := expressionError("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, target.Usage.Type.DiagnosticName(), expr.ResultUsage().Type.DiagnosticName()), expr.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	return coercionExpression{b.info(target.Usage, expr.SourcePosition(), expr.Properties()), expr, plan, sqltype.Assignment}, nil
}
