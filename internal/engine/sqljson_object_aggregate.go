package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSQLJSONObjectAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	args, usage, returning, err := b.resolveSQLJSONObjectAggregate(raw, name)
	if err != nil {
		return nil, err
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	if b.defaultConsumer {
		return nil, expressionError("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
	}
	operation := aggregateJSONObject
	if raw.JSON.Absent {
		operation = aggregateJSONObjectStrict
	}
	provider := jsonObjectAggregateProvider{usage: usage, strict: raw.JSON.Absent, unique: raw.JSON.Unique}
	call := aggregateCall{provider: provider, argument: args[0], separator: args[1], operation: operation, includeNulls: true}
	// SQL/JSON syntax lowers to the genuine aggregate selected by its output
	// format and NULL/unique options, not to a json_objectagg pg_proc entry.
	routineName := "json_object_agg"
	if usage.Type.ID() == sqltype.JSONB {
		routineName = "jsonb_object_agg"
	}
	if raw.JSON.Unique {
		routineName += "_unique"
	}
	if raw.JSON.Absent {
		routineName += "_strict"
	}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, routineName, sqltype.ID(2276), sqltype.ID(2276))
	if err != nil {
		return nil, err
	}
	node, err := b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{StageDependent: true}))
	if err != nil {
		return nil, err
	}
	node, err = b.ownAggregate(node)
	if err != nil {
		return nil, err
	}
	if returning.Type == nil {
		return node, nil
	}
	if err = b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, node.Properties()), node)
	if err != nil {
		return nil, err
	}
	plan, err := jsonReturningCoercion(node.ResultUsage(), info.Usage, raw.JSON.Output)
	if err != nil {
		return nil, err
	}
	info.Property = coercionProperties(node, plan)
	return coercionExpression{info, node, plan, sqltype.Assignment, false}, nil
}

// Resolve children and JSON options before FILTER, but leave the final assignment
// coercion to the caller: PostgreSQL diagnoses FILTER before an invalid final cast.
// Window admission shares this analysis without registering executable group state.
func (b *expressionBinder) resolveSQLJSONObjectAggregate(raw parser.AggregateExpr, name parser.ObjectName) ([2]BoundExpr, sqltype.Usage, sqltype.Usage, error) {
	var operands [2]comparisonOperand
	var types [2]string
	var args [2]BoundExpr
	var returning sqltype.Usage
	fail := func(err error) ([2]BoundExpr, sqltype.Usage, sqltype.Usage, error) {
		return args, sqltype.Usage{}, sqltype.Usage{}, err
	}
	var err error
	for i, child := range raw.Args {
		operands[i], err = b.comparisonOperand(child)
		if err != nil {
			return fail(err)
		}
		types[i] = "unknown"
		if d := operands[i].descriptor(); d != nil {
			types[i] = b.diagnosticTypeName(d)
		}
		// SQL/JSON values resolve unknowns to text; keys retain ANY inference.
		if i == 1 && operands[i].descriptor() == nil {
			operands[i].node, err = b.finalizeOperand(operands[i], b.typ(sqltype.Text))
			if err != nil {
				return fail(err)
			}
		}
	}
	args, _, err = b.resolveJSONObjectAggregateSignature(raw, name, operands[:], types[:])
	if err != nil {
		return fail(err)
	}
	options := raw.JSON
	if options.Input != nil {
		format := options.Input
		arg := args[1]
		base := arg.ResultUsage().Type.BaseType()
		if format.Encoding != "" && base.ID() != sqltype.Bytea {
			return fail(expressionError("42804", "JSON ENCODING clause is only allowed for bytea input type", format.Pos))
		}
		if base.ID() != sqltype.JSON && base.ID() != sqltype.JSONB {
			if base.ID() != sqltype.Bytea && !sqltype.StringCategory(base.ID()) {
				return fail(expressionError("42804", "cannot use non-string types with explicit FORMAT JSON clause", format.Pos))
			}
			plan, err := sqltype.PlanJSONFormattedInput(arg.ResultUsage(), b.typ(sqltype.JSON).Usage(), format.Encoding)
			if err != nil {
				return fail(err)
			}
			info, err := b.coercionInfo(b.info(b.typ(sqltype.JSON).Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg)
			if err != nil {
				return fail(err)
			}
			args[1] = coercionExpression{info, arg, plan, sqltype.Explicit, false}
		}
	}
	id := sqltype.JSON
	if options.Returning != nil {
		if options.Returning.Setof {
			return fail(sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions"))
		}
		returning, err = b.engine.resolveAnnotationTypeInput(*options.Returning, b.op, false, false, true, true, false)
		if err != nil {
			return fail(err)
		}
		if jsonReturningPseudoType(returning.Type) {
			return fail(sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions"))
		}
		if returning.Type.ID() == sqltype.JSONB {
			id = sqltype.JSONB
		}
	} else {
		for _, arg := range args {
			if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ID() == sqltype.JSONB {
				id = sqltype.JSONB
			}
		}
	}
	if options.Output != nil {
		id = sqltype.JSON
		if err := validateJSONOutputFormat(returning, options.Output); err != nil {
			return fail(err)
		}
	}
	return args, b.typ(id).Usage(), returning, nil
}
