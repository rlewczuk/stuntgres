package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type jsonObjectAggregateProvider struct {
	key, input, usage sqltype.Usage
	strict, unique    bool
	plan              sqltype.JSONObjectAggregatePlan
}

func (p jsonObjectAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p jsonObjectAggregateProvider) Init(ctx context.Context) (any, error) {
	return p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{})
}
func (p jsonObjectAggregateProvider) InitWithEnvironment(ctx context.Context, env sqltype.OutputEnvironment) (any, error) {
	return p.plan.Init(ctx, env)
}
func (p jsonObjectAggregateProvider) Advance(ctx context.Context, state any, key, value sqltype.Value) error {
	s, ok := state.(*sqltype.JSONObjectAggregateState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid JSON object aggregate state")
	}
	return p.plan.Advance(ctx, s, key, value)
}
func (p jsonObjectAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, ok := state.(*sqltype.JSONObjectAggregateState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid JSON object aggregate state")
	}
	return p.plan.Finalize(ctx, s)
}

func jsonObjectAggregateName(name string) bool {
	switch name {
	case "json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict",
		"json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict", "json_objectagg":
		return true
	}
	return false
}

func (b *expressionBinder) bindJSONObjectAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	if raw.JSON != nil {
		return b.bindSQLJSONObjectAggregate(raw, name)
	}
	var small [2]comparisonOperand
	var names [2]string
	operands, types := small[:], names[:]
	if len(raw.Args) <= 2 {
		operands, types = operands[:len(raw.Args)], types[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands, types = make([]comparisonOperand, len(raw.Args)), make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
			types[i] = "unknown"
			if d := o.descriptor(); d != nil {
				types[i] = b.diagnosticTypeName(d)
			}
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	args, usage, err := b.resolveJSONObjectAggregateSignature(raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	if b.defaultConsumer {
		return nil, expressionError("42803", "aggregate functions are not allowed in DEFAULT expressions", raw.Pos)
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
	operation := functions.AggregateJSONObject
	if strict {
		operation = functions.AggregateJSONObjectStrict
	}
	provider := jsonObjectAggregateProvider{usage: usage, strict: strict, unique: unique}
	call := aggregateCall{provider: provider, argument: args[0], separator: args[1], operation: operation, includeNulls: true}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, sqltype.ID(2276), sqltype.ID(2276))
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{StageDependent: true}))
}

// Ordinary and unsupported-window paths share only signature/inference analysis;
// this does not register an aggregate or allocate mutable execution state.
func (b *expressionBinder) resolveJSONObjectAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operands []comparisonOperand, types []string) ([2]BoundExpr, sqltype.Usage, error) {
	var args [2]BoundExpr
	if len(raw.Args) > 100 {
		return args, sqltype.Usage{}, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible := true
	var err error
	if raw.JSON == nil {
		visible, err = b.builtinVisible(name, functionLookup)
		if err != nil {
			return args, sqltype.Usage{}, err
		}
	}
	if (raw.JSON == nil && (!visible || raw.Name == "json_objectagg")) || len(raw.Args) != 2 || raw.Star {
		return args, sqltype.Usage{}, b.functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	for i, o := range operands {
		arg := o.node
		if literal, ok := raw.Args[i].(parser.LiteralExpr); ok && o.descriptor() == nil && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			arg, err = b.finalizeOperand(o, b.typ(sqltype.Text))
			if err != nil {
				return args, sqltype.Usage{}, err
			}
		}
		if arg == nil {
			return args, sqltype.Usage{}, fmt.Errorf("stuntgres: missing JSON object aggregate argument")
		}
		if parameter, ok := arg.(parameterExpression); ok && parameter.Usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
		args[i] = arg
	}
	id := sqltype.JSON
	switch raw.Name {
	case "jsonb_object_agg", "jsonb_object_agg_strict", "jsonb_object_agg_unique", "jsonb_object_agg_unique_strict":
		id = sqltype.JSONB
	}
	return args, b.typ(id).Usage(), nil
}

// A modifier resolves one particular ANY input occurrence, not every use of its
// parameter number. Keep earlier occurrences for finish's 42P08 validation.
func (b *expressionBinder) completeJSONObjectAggregateInput(node BoundExpr) (BoundExpr, error) {
	p, unknown := node.(parameterExpression)
	if !unknown || p.Usage.Type != nil {
		return node, nil
	}
	resolved, err := b.finalizeProjection(node)
	if err != nil {
		return nil, err
	}
	for i, prior := range b.anyParameters {
		if prior.number != p.number || prior.Pos != p.Pos {
			continue
		}
		copy(b.anyParameters[i:], b.anyParameters[i+1:])
		last := len(b.anyParameters) - 1
		b.anyParameters[last] = parameterExpression{}
		b.anyParameters = b.anyParameters[:last]
		break
	}
	return resolved, nil
}

func (p jsonObjectAggregateProvider) complete(key, input sqltype.Usage) (jsonObjectAggregateProvider, error) {
	p.key, p.input = key, input
	// Unresolved occurrences are never executable; binder.finish diagnoses them
	// after the whole query has supplied its occurrence-sensitive constraints.
	if key.Type == nil || input.Type == nil {
		return p, nil
	}
	var err error
	p.plan, err = sqltype.BindJSONObjectAggregate(key, input, p.usage, p.strict, p.unique)
	return p, err
}
