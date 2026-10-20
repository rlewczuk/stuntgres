package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSQLJSONArrayQuery(raw parser.SQLJSONExpr) (expr.Expr, error) {
	if raw.Kind != "json_array" {
		return nil, expr.ErrorAt("42601", "syntax error at or near \"SELECT\"", raw.Pos)
	}
	bound, err := b.bindSubquery(parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: raw.Query, Pos: raw.Pos})
	if err != nil {
		return nil, err
	}
	query := bound.(subqueryExpression)
	project := query.queryPlan.Root.(*projectNode)
	arg := expr.Expr(columnExpression{Info: b.info(query.Usage, raw.Pos, expr.Properties{RowDependent: true}), scope: 1, frame: project.frame, slot: 0})
	var format *parser.JSONFormat
	if len(raw.Formats) > 0 {
		format = raw.Formats[0]
	}
	arg, err = functions.SQLJSONInput(b, arg, format, false, false)
	if err != nil {
		return nil, err
	}
	id := sqltype.JSON
	if arg.ResultUsage().Type.ID() == sqltype.JSONB {
		id = sqltype.JSONB
	}
	var returning sqltype.Usage
	if raw.Returning != nil {
		returning, err = functions.SQLJSONReturningType(b, *raw.Returning)
		if err != nil {
			return nil, err
		}
		id = sqltype.JSON
		if returning.Type.ID() == sqltype.JSONB {
			id = sqltype.JSONB
		}
	}
	if raw.Output != nil {
		id = sqltype.JSON
	}
	if err := functions.ValidateJSONOutputFormat(returning, raw.Output); err != nil {
		return nil, err
	}
	usage := b.typ(id).Usage()
	plan, err := sqltype.BindJSONArrayAggregate(arg.ResultUsage(), usage, true)
	if err != nil {
		return nil, err
	}
	query.jsonArray = &sqlJSONArrayQuery{raw: raw, input: arg, plan: plan}
	query.Usage = usage
	if returning.Type == nil {
		return query, nil
	}
	coercion, err := functions.JSONReturningCoercion(usage, returning, raw.Output)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, coercionProperties(query, coercion)), query)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, query, coercion, sqltype.Assignment, false}, nil
}
func (c *EvalContext) subqueryJSONArray(e subqueryExpression, state *queryState, stage *indexedStage) (sqltype.Value, error) {
	p := e.jsonArray
	aggregate, err := p.plan.Init(c.Context, c.FunctionJSONOutput())
	if err != nil {
		return sqltype.Value{}, err
	}
	var row [1]sqltype.Value
	for {
		present, err := stage.Next(c.Context, row[:])
		if err != nil {
			return sqltype.Value{}, err
		}
		if !present {
			return p.plan.Finalize(c.Context, aggregate)
		}
		inner := state.evaluation(c.Context)
		inner.Frame = e.queryPlan.Root.(*projectNode).frame
		inner.Row = row[:]
		value, err := p.input.Eval(&inner)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err = p.plan.Advance(c.Context, aggregate, value); err != nil {
			return sqltype.Value{}, err
		}
	}
}
