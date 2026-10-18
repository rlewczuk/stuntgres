package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type sqlJSONArrayQuery struct {
	raw   parser.SQLJSONExpr
	input BoundExpr
	plan  sqltype.JSONArrayAggregatePlan
}

func (b *expressionBinder) bindSQLJSONArrayQuery(raw parser.SQLJSONExpr) (BoundExpr, error) {
	if raw.Kind != "json_array" {
		return nil, expressionError("42601", "syntax error at or near \"SELECT\"", raw.Pos)
	}
	bound, err := b.bindSubquery(parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: raw.Query, Pos: raw.Pos})
	if err != nil {
		return nil, err
	}
	query := bound.(subqueryExpression)
	project := query.queryPlan.Root.(*projectNode)
	arg := BoundExpr(columnExpression{expressionInfo: b.info(query.Usage, raw.Pos, ExprProperties{RowDependent: true}), scope: 1, frame: project.frame, slot: 0})
	var format *parser.JSONFormat
	if len(raw.Formats) > 0 {
		format = raw.Formats[0]
	}
	arg, err = b.sqlJSONInput(arg, format, false, false)
	if err != nil {
		return nil, err
	}
	id := sqltype.JSON
	if arg.ResultUsage().Type.ID() == sqltype.JSONB {
		id = sqltype.JSONB
	}
	var returning sqltype.Usage
	if raw.Returning != nil {
		if raw.Returning.Setof {
			return nil, sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions")
		}
		returning, err = b.engine.resolveAnnotationTypeInput(*raw.Returning, b.op, false, false, true, true, false)
		if err != nil {
			return nil, err
		}
		if functions.JSONReturningPseudoType(returning.Type) {
			return nil, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
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
func (e subqueryExpression) evalJSONArray(c *EvalContext, state *queryState, stage *indexedStage) (sqltype.Value, error) {
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
