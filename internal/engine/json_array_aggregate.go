package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func jsonArrayAggregateName(name string) bool {
	switch name {
	case "json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg":
		return true
	}
	return false
}

func jsonReturningPseudoType(d *sqltype.Descriptor) bool {
	if d == nil || !d.IsBuiltin() {
		return false
	}
	switch d.Name() {
	case "record", "cstring", "any", "anyarray", "anyelement", "anynonarray", "anyenum", "anyrange", "anymultirange", "anycompatible", "anycompatiblearray", "anycompatiblenonarray", "anycompatiblerange", "anycompatiblemultirange", "void", "trigger", "event_trigger", "language_handler", "internal", "fdw_handler", "index_am_handler", "table_am_handler", "tsm_handler", "pg_ddl_command":
		return true
	}
	return false
}

func (b *expressionBinder) bindJSONArrayAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var one [1]string
	types := one[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	for i, child := range raw.Args {
		var err error
		operand, err = b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		types[i] = "unknown"
		if d := operand.descriptor(); d != nil {
			types[i] = b.diagnosticTypeName(d)
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, usage, returning, err := b.resolveJSONArrayAggregateSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	if b.defaultConsumer {
		return nil, expressionError("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
	}
	absent := raw.Name == "json_agg_strict" || raw.Name == "jsonb_agg_strict"
	operation := aggregateJSON
	if absent {
		operation = aggregateJSONStrict
	}
	if raw.JSON != nil {
		absent = raw.JSON.Absent
		operation = aggregateJSONArray
		if absent {
			operation = aggregateJSONArrayAbsent
		}
	}
	arrayPlan, err := sqltype.BindJSONArrayAggregate(arg.ResultUsage(), usage, absent)
	if err != nil {
		return nil, err
	}
	provider := functions.NewJSONArrayAggregateProvider(arg.ResultUsage(), usage, absent, arrayPlan)
	call := aggregateCall{provider: provider, argument: arg, operation: operation, includeNulls: true}
	routineName := "json_agg"
	switch {
	case usage.Type.ID() == sqltype.JSONB && absent:
		routineName = "jsonb_agg_strict"
	case usage.Type.ID() == sqltype.JSONB:
		routineName = "jsonb_agg"
	case absent:
		routineName = "json_agg_strict"
	}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, routineName, sqltype.ID(2283))
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

func jsonAggregateFormatSQL(format *parser.JSONFormat) string {
	if format == nil {
		return ""
	}
	text := " FORMAT JSON"
	if format.Encoding != "" {
		text += " ENCODING " + format.Encoding
	}
	return text
}

// Ordinary and window admission share the same type/FORMAT resolution. This
// helper never registers group state or changes an aggregate's lexical owner.
func (b *expressionBinder) resolveJSONArrayAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, sqltype.Usage, sqltype.Usage, error) {
	var arg BoundExpr
	var returning sqltype.Usage
	var err error
	fail := func(err error) (BoundExpr, sqltype.Usage, sqltype.Usage, error) {
		return nil, sqltype.Usage{}, sqltype.Usage{}, err
	}
	if raw.JSON == nil {
		arg, err = b.resolveAnyValueSignature(raw, name, operand.node, sqltype.Usage{Type: operand.descriptor()}, types)
	} else {
		arg = operand.node
		if operand.descriptor() == nil {
			arg, err = b.finalizeOperand(operand, b.typ(sqltype.Text))
		}
	}
	if err != nil {
		return fail(err)
	}
	if arg == nil {
		return fail(fmt.Errorf("stuntgres: missing JSON aggregate argument"))
	}
	if arg.ResultUsage().Type.ContainsCatalogIdentifier() {
		b.regclassConcat = true
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
			returning, err = b.engine.resolveAnnotationTypeInput(*options.Returning, b.op, false, false, true, true, false)
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
			if err := validateJSONOutputFormat(returning, options.Output); err != nil {
				return fail(err)
			}
		}
		if options.Input != nil {
			format := options.Input
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
				arg = coercionExpression{info, arg, plan, sqltype.Explicit, false}
			}
		}
	}
	usage := b.typ(id).Usage()
	return arg, usage, returning, nil
}

// Encoding requires the resolved BYTEA identity, not a domain over BYTEA.
// Owners call this before output planning, preserving their input error order.
func validateJSONOutputFormat(returning sqltype.Usage, format *parser.JSONFormat) error {
	if format == nil || format.Encoding == "" {
		return nil
	}
	if returning.Type == nil || returning.Type.ID() != sqltype.Bytea {
		return expressionError("0A000", "cannot set JSON encoding for non-bytea output types", format.Pos)
	}
	if format.Encoding != "UTF8" {
		d := expressionError("0A000", "unsupported JSON encoding", format.Pos).(*sqlstate.Error)
		d.Hint = "Only UTF8 JSON encoding is supported."
		return d
	}
	return nil
}

func jsonReturningCoercion(source, target sqltype.Usage, format *parser.JSONFormat) (sqltype.CoercionPlan, error) {
	plan, err := sqltype.PlanJSONReturning(source, target)
	if d, ok := err.(*sqlstate.Error); ok && format != nil {
		copy := *d
		copy.Position = fmt.Sprint(format.Pos.Character)
		err = &copy
	}
	return plan, err
}
