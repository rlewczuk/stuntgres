package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
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
	query := bound.(plan.SubqueryExpr)
	project := query.QueryPlan.Root.(*plan.ProjectNode)
	arg := expr.Expr(plan.ColumnExpr{Info: b.info(query.Usage, raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: project.Frame, Slot: 0})
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
	jsonArrayAggregatePlan, err := sqltype.BindJSONArrayAggregate(arg.ResultUsage(), usage, true)
	if err != nil {
		return nil, err
	}
	query.JSONArray = &plan.SQLJSONArrayQuery{Raw: raw, Input: arg, Plan: jsonArrayAggregatePlan}
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
	return plan.CoercionExpr{Info: info, Child: query, Plan: coercion, Mode: sqltype.Assignment}, nil
}
