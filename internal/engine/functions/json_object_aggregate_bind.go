package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindJSONObjectAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	if raw.JSON != nil {
		return bindSQLJSONObjectAggregate(b, raw, name)
	}
	var small [2]Argument
	var names [2]string
	operands, types := small[:], names[:]
	if len(raw.Args) <= 2 {
		operands, types = operands[:len(raw.Args)], types[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands, types = make([]Argument, len(raw.Args)), make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		if i < len(operands) {
			operands[i] = o
			types[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	args, usage, err := resolveJSONObjectAggregateSignature(b, raw, name, operands, types)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	if b.DefaultConsumer() {
		return nil, sqltype.Usage{}, expr.ErrorAt("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
	}
	strict := false
	switch raw.Name {
	case "json_object_agg_strict", "jsonb_object_agg_strict", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict":
		strict = true
	}
	unique := false
	switch raw.Name {
	case "json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict":
		unique = true
	}
	operation := AggregateJSONObject
	if strict {
		operation = AggregateJSONObjectStrict
	}
	call := AggregateCall{Provider: NewJSONObjectAggregate(usage, strict, unique), Arguments: []expr.Expr{args[0], args[1]}, Operation: operation, IncludeNulls: true}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, sqltype.ID(2276), sqltype.ID(2276))
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	node, err := b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{StageDependent: true}))
	return node, sqltype.Usage{}, err
}

// resolveJSONObjectAggregateSignature is the signature and inference
// analysis that ordinary and window calls share; it registers no aggregate.
func resolveJSONObjectAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operands []Argument, types []string) ([2]expr.Expr, sqltype.Usage, error) {
	var args [2]expr.Expr
	if len(raw.Args) > 100 {
		return args, sqltype.Usage{}, aggregateLimitError(raw.Pos)
	}
	visible := true
	var err error
	if raw.JSON == nil {
		visible, err = b.BuiltinVisible(name)
		if err != nil {
			return args, sqltype.Usage{}, err
		}
	}
	if (raw.JSON == nil && (!visible || raw.Name == "json_objectagg")) || len(raw.Args) != 2 || raw.Star {
		return args, sqltype.Usage{}, b.SignatureError(name, raw.Pos, types)
	}
	for i, o := range operands {
		arg := o.Expression
		if literal, ok := o.Raw.(parser.LiteralExpr); ok && o.Usage.Type == nil && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			arg, err = finalizeOperand(b, o, b.Type(sqltype.Text))
			if err != nil {
				return args, sqltype.Usage{}, err
			}
		}
		if arg == nil {
			return args, sqltype.Usage{}, fmt.Errorf("stuntgres: missing JSON object aggregate argument")
		}
		b.RetainUntypedParameter(arg)
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
		args[i] = arg
	}
	id := sqltype.JSON
	switch raw.Name {
	case "jsonb_object_agg", "jsonb_object_agg_strict", "jsonb_object_agg_unique", "jsonb_object_agg_unique_strict":
		id = sqltype.JSONB
	}
	return args, b.Type(id).Usage(), nil
}

func bindSQLJSONObjectAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	args, usage, returning, err := resolveSQLJSONObjectAggregate(b, raw, name)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	if b.DefaultConsumer() {
		return nil, sqltype.Usage{}, expr.ErrorAt("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
	}
	operation := AggregateJSONObject
	if raw.JSON.Absent {
		operation = AggregateJSONObjectStrict
	}
	provider := NewJSONObjectAggregate(usage, raw.JSON.Absent, raw.JSON.Unique)
	call := AggregateCall{Provider: provider, Arguments: []expr.Expr{args[0], args[1]}, Operation: operation, IncludeNulls: true}
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
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, routineName, sqltype.ID(2276), sqltype.ID(2276))
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	node, err := b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{StageDependent: true}))
	return node, returning, err
}

// resolveSQLJSONObjectAggregate resolves children and JSON options before
// FILTER, leaving the final RETURNING coercion to the caller: PostgreSQL
// diagnoses FILTER before an invalid final cast. Window calls share it.
func resolveSQLJSONObjectAggregate(b Binder, raw parser.AggregateExpr, name parser.ObjectName) ([2]expr.Expr, sqltype.Usage, sqltype.Usage, error) {
	var operands [2]Argument
	var types [2]string
	var args [2]expr.Expr
	var returning sqltype.Usage
	fail := func(err error) ([2]expr.Expr, sqltype.Usage, sqltype.Usage, error) {
		return args, sqltype.Usage{}, sqltype.Usage{}, err
	}
	var err error
	for i, child := range raw.Args {
		operands[i], err = comparisonOperand(b, child)
		if err != nil {
			return fail(err)
		}
		types[i] = b.DiagnosticTypeName(operands[i].Usage.Type)
		// SQL/JSON values resolve unknowns to text; keys retain ANY inference.
		if i == 1 && operands[i].Usage.Type == nil {
			node, err := finalizeOperand(b, operands[i], b.Type(sqltype.Text))
			if err != nil {
				return fail(err)
			}
			operands[i].Expression, operands[i].Usage, operands[i].Kind = node, node.ResultUsage(), BoundArgument
		}
	}
	args, _, err = resolveJSONObjectAggregateSignature(b, raw, name, operands[:], types[:])
	if err != nil {
		return fail(err)
	}
	options := raw.JSON
	if options.Input != nil {
		args[1], err = SQLJSONInput(b, args[1], options.Input, false, false)
		if err != nil {
			return fail(err)
		}
	}
	id := sqltype.JSON
	if options.Returning != nil {
		if options.Returning.Setof {
			return fail(sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions"))
		}
		returning, err = b.JSONReturningType(*options.Returning)
		if err != nil {
			return fail(err)
		}
		if JSONReturningPseudoType(returning.Type) {
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
		if err := ValidateJSONOutputFormat(returning, options.Output); err != nil {
			return fail(err)
		}
	}
	return args, b.Type(id).Usage(), returning, nil
}

func windowJSONObjectAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	args, usage, err := resolveJSONObjectAggregateSignature(b, raw, name, operands, types)
	return args[0], usage, err
}

// windowSQLJSONObjectAggregate resolves JSON_OBJECTAGG with OVER: options,
// then FILTER, then the RETURNING conversion. Without SQL/JSON syntax the
// name has no ordinary signature.
func windowSQLJSONObjectAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	if raw.JSON == nil {
		_, types, err := windowAggregateOperands(b, raw, plainTypeName)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		return nil, sqltype.Usage{}, ResolutionError(name, raw.Pos, types, false)
	}
	_, usage, returning, err := resolveSQLJSONObjectAggregate(b, raw, name)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	if _, err = b.AnalyzeAggregateFilter(raw); err != nil {
		return nil, sqltype.Usage{}, err
	}
	if returning.Type != nil {
		_, err = JSONReturningCoercion(usage, returning, raw.JSON.Output)
		usage = returning
	}
	return nil, usage, err
}
