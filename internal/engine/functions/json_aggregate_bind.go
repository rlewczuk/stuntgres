package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var jsonArrayAggregateNames = [...]string{"json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg"}

var jsonObjectAggregateNames = [...]string{
	"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict",
	"json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict",
}

// JSONArrayAggregateName reports the JSON array aggregates, including the
// SQL/JSON JSON_ARRAYAGG.
func JSONArrayAggregateName(name string) bool {
	for _, candidate := range jsonArrayAggregateNames {
		if candidate == name {
			return true
		}
	}
	return false
}

// JSONObjectAggregateName reports the JSON object aggregates, including the
// SQL/JSON JSON_OBJECTAGG.
func JSONObjectAggregateName(name string) bool {
	for _, candidate := range jsonObjectAggregateNames {
		if candidate == name {
			return true
		}
	}
	return name == "json_objectagg"
}

func bindJSONArrayAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	var one [1]string
	types := one[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand Argument
	for i, child := range raw.Args {
		var err error
		operand, err = comparisonOperand(b, child)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		types[i] = b.DiagnosticTypeName(operand.Usage.Type)
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, usage, returning, err := resolveJSONArrayAggregateSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	if b.DefaultConsumer() {
		return nil, sqltype.Usage{}, expr.ErrorAt("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
	}
	absent := raw.Name == "json_agg_strict" || raw.Name == "jsonb_agg_strict"
	operation := AggregateJSON
	if absent {
		operation = AggregateJSONStrict
	}
	if raw.JSON != nil {
		absent = raw.JSON.Absent
		operation = AggregateJSONArray
		if absent {
			operation = AggregateJSONArrayAbsent
		}
	}
	arrayPlan, err := sqltype.BindJSONArrayAggregate(arg.ResultUsage(), usage, absent)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	provider := NewJSONArrayAggregateProvider(arg.ResultUsage(), usage, absent, arrayPlan)
	call := AggregateCall{Provider: provider, Argument: arg, Operation: operation, IncludeNulls: true}
	routineName := "json_agg"
	switch {
	case usage.Type.ID() == sqltype.JSONB && absent:
		routineName = "jsonb_agg_strict"
	case usage.Type.ID() == sqltype.JSONB:
		routineName = "jsonb_agg"
	case absent:
		routineName = "json_agg_strict"
	}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, routineName, sqltype.ID(2283))
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	node, err := b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{StageDependent: true}))
	return node, returning, err
}

// resolveJSONArrayAggregateSignature is the type and FORMAT resolution that
// ordinary and window calls share. It never registers group state.
func resolveJSONArrayAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, sqltype.Usage, sqltype.Usage, error) {
	var arg expr.Expr
	var returning sqltype.Usage
	var err error
	fail := func(err error) (expr.Expr, sqltype.Usage, sqltype.Usage, error) {
		return nil, sqltype.Usage{}, sqltype.Usage{}, err
	}
	if raw.JSON == nil {
		arg, err = ResolveAnyValueSignature(b, raw, name, operand.Expression, sqltype.Usage{Type: operand.Usage.Type}, types)
	} else {
		arg = operand.Expression
		if operand.Usage.Type == nil {
			arg, err = finalizeOperand(b, operand, b.Type(sqltype.Text))
		}
	}
	if err != nil {
		return fail(err)
	}
	if arg == nil {
		return fail(fmt.Errorf("stuntgres: missing JSON aggregate argument"))
	}
	if arg.ResultUsage().Type.ContainsCatalogIdentifier() {
		b.NoteCatalogText()
	}
	id := sqltype.JSON
	if raw.Name == "jsonb_agg" || raw.Name == "jsonb_agg_strict" {
		id = sqltype.JSONB
	}
	if options := raw.JSON; options != nil {
		if options.Returning != nil {
			if options.Returning.Setof {
				return fail(sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions"))
			}
			returning, err = b.JSONReturningType(*options.Returning)
			if err != nil {
				return fail(err)
			}
			if returning.Type.ID() == sqltype.JSONB {
				id = sqltype.JSONB
			}
		} else if arg.ResultUsage().Type.ID() == sqltype.JSONB {
			id = sqltype.JSONB
		}
		if options.Output != nil {
			id = sqltype.JSON
			if err := ValidateJSONOutputFormat(returning, options.Output); err != nil {
				return fail(err)
			}
		}
		if options.Input != nil {
			arg, err = SQLJSONInput(b, arg, options.Input, false, false)
			if err != nil {
				return fail(err)
			}
		}
	}
	return arg, b.Type(id).Usage(), returning, nil
}

func windowJSONArrayAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, usage, returning, err := resolveJSONArrayAggregateSignature(b, raw, name, firstOperand(operands), types)
	if err == nil && returning.Type != nil {
		_, err = JSONReturningCoercion(usage, returning, raw.JSON.Output)
		usage = returning
	}
	return arg, usage, err
}
