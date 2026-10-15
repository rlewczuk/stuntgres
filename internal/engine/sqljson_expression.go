package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type sqlJSONExpression struct {
	expressionInfo
	raw  parser.SQLJSONExpr
	args []BoundExpr
	plan sqltype.SQLJSONPlan
}

func (e sqlJSONExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var small [8]sqltype.Value
	values := small[:min(len(e.args), len(small))]
	if len(e.args) > len(small) {
		values = make([]sqltype.Value, len(e.args))
	}
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Apply(c.Context, values, c.FunctionJSONOutput())
}
func sqlJSONKind(name string) sqltype.SQLJSONKind {
	switch name {
	case "json_array":
		return sqltype.SQLJSONArray
	case "json_object":
		return sqltype.SQLJSONObject
	case "json":
		return sqltype.SQLJSONParse
	case "json_scalar":
		return sqltype.SQLJSONScalar
	case "json_serialize":
		return sqltype.SQLJSONSerialize
	}
	return 0
}
func (e sqlJSONExpression) reconstruct(info expressionInfo, args []BoundExpr) (BoundExpr, error) {
	var small [8]sqltype.Usage
	usages := small[:min(len(args), len(small))]
	if len(args) > len(small) {
		usages = make([]sqltype.Usage, len(args))
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindSQLJSON(sqlJSONKind(e.raw.Kind), info.usage, usages, e.raw.Absent, e.raw.Unique && e.raw.Kind != "json")
	if err != nil {
		return nil, err
	}
	e.expressionInfo, e.args, e.plan = info, args, plan
	return e, nil
}
func (b *expressionBinder) sqlJSONInput(arg BoundExpr, format *parser.JSONFormat, implicit, unique bool) (BoundExpr, error) {
	base := arg.ResultUsage().Type.BaseType()
	if format == nil && !implicit {
		return arg, nil
	}
	pos := arg.SourcePosition()
	encoding := ""
	if format != nil {
		pos = format.Pos
		encoding = format.Encoding
	}
	if encoding != "" && base.ID() != sqltype.Bytea {
		return nil, expressionError("42804", "JSON ENCODING clause is only allowed for bytea input type", pos)
	}
	if base.ID() == sqltype.JSON || base.ID() == sqltype.JSONB {
		return arg, nil
	}
	if base.ID() != sqltype.Bytea && !sqltype.StringCategory(base.ID()) {
		if format != nil {
			return nil, expressionError("42804", "cannot use non-string types with explicit FORMAT JSON clause", pos)
		}
		return nil, expressionError("42846", "cannot cast type "+base.DiagnosticName()+" to json", arg.SourcePosition())
	}
	plan, err := sqltype.PlanJSONFormattedInput(arg.ResultUsage(), b.typ(sqltype.JSON).Usage(), encoding)
	if err != nil {
		return nil, err
	}
	if unique {
		plan, err = sqltype.PlanJSONUniqueInput(arg.ResultUsage(), b.typ(sqltype.JSON).Usage(), encoding)
		if err != nil {
			return nil, err
		}
	}
	info, err := b.coercionInfo(b.info(b.typ(sqltype.JSON).Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, arg, plan, sqltype.Explicit, false}, nil
}
func (b *expressionBinder) bindSQLJSON(raw parser.SQLJSONExpr) (BoundExpr, error) {
	kind := sqlJSONKind(raw.Kind)
	if kind == 0 {
		return nil, fmt.Errorf("invalid SQL/JSON constructor")
	}
	if raw.Query != nil {
		return b.bindSQLJSONArrayQuery(raw)
	}
	if kind >= sqltype.SQLJSONParse && len(raw.Args) != 1 || kind == sqltype.SQLJSONObject && len(raw.Args)%2 != 0 {
		return nil, expressionError("42601", "invalid SQL/JSON constructor arguments", raw.Pos)
	}
	if kind == sqltype.SQLJSONScalar && raw.Returning != nil {
		return nil, expressionError("42601", "syntax error at or near \"RETURNING\"", raw.Returning.Pos)
	}
	var returning sqltype.Usage
	var err error
	if raw.Returning != nil {
		if raw.Returning.Setof {
			return nil, sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions")
		}
		returning, err = b.engine.resolveAnnotationTypeInput(*raw.Returning, b.op, false, false, true, true)
		if err != nil {
			return nil, err
		}
		if jsonReturningPseudoType(returning.Type) {
			return nil, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
		}
	}
	args := make([]BoundExpr, len(raw.Args))
	properties := ExprProperties{Volatility: Immutable}
	id := sqltype.JSON
	for i, child := range raw.Args {
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		arg := operand.node
		if kind == sqltype.SQLJSONObject && i%2 == 0 && operand.descriptor() == nil {
			if parameter, ok := arg.(parameterExpression); ok {
				b.anyParameters = append(b.anyParameters, parameter)
				args[i] = arg
				properties = mergeExpressionProperties(properties, arg.Properties())
				continue
			}
		}
		if operand.descriptor() == nil {
			arg, err = b.finalizeOperand(operand, b.typ(sqltype.Text))
			if err != nil {
				return nil, err
			}
		}
		if raw.Unique && kind == sqltype.SQLJSONParse && arg.ResultUsage().Type.BaseType().ID() != sqltype.Bytea && !sqltype.StringCategory(arg.ResultUsage().Type.BaseType().ID()) {
			return nil, expressionError("42804", "cannot use non-string types with WITH UNIQUE KEYS clause", raw.Pos)
		}
		var format *parser.JSONFormat
		if i < len(raw.Formats) {
			format = raw.Formats[i]
		}
		arg, err = b.sqlJSONInput(arg, format, kind == sqltype.SQLJSONParse || kind == sqltype.SQLJSONSerialize, raw.Unique && kind == sqltype.SQLJSONParse)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage().Type.ID() == sqltype.JSONB && !(kind == sqltype.SQLJSONObject && i%2 == 0) {
			id = sqltype.JSONB
		}
		args[i] = arg
		if arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
		properties = mergeExpressionProperties(properties, arg.Properties())
		base := arg.ResultUsage().Type.BaseType()
		if kind == sqltype.SQLJSONScalar || kind == sqltype.SQLJSONArray || kind == sqltype.SQLJSONObject {
			if base.IsComposite() || base.OutputSessionDependent() || base.ID() == sqltype.Date || base.ID() == sqltype.Timestamp || base.ID() == sqltype.Timestamptz {
				properties.Volatility = max(properties.Volatility, Stable)
			}
		}
	}
	if err := validateJSONOutputFormat(returning, raw.Output); err != nil {
		return nil, err
	}
	if kind == sqltype.SQLJSONSerialize {
		if returning.Type == nil {
			returning = b.typ(sqltype.Text).Usage()
		}
		base := returning.Type.BaseType()
		if base.ID() != sqltype.Bytea && !sqltype.StringCategory(base.ID()) {
			d := sqlError("42804", "cannot use type "+returning.Type.DiagnosticName()+" in RETURNING clause of JSON_SERIALIZE()").(*sqlstate.Error)
			d.Hint = "Try returning a string type or bytea."
			return nil, d
		}
	}
	if kind >= sqltype.SQLJSONParse {
		id = sqltype.JSON
	}
	if returning.Type != nil {
		id = sqltype.JSON
		if returning.Type.ID() == sqltype.JSONB {
			id = sqltype.JSONB
		}
	}
	if raw.Output != nil {
		id = sqltype.JSON
	}
	usage := b.typ(id).Usage()
	node, err := (sqlJSONExpression{raw: raw}).reconstruct(b.info(usage, raw.Pos, properties), args)
	if err != nil {
		return nil, err
	}
	if returning.Type == nil {
		return node, nil
	}
	if err = b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	plan, err := jsonReturningCoercion(usage, returning, raw.Output)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, coercionProperties(node, plan)), node)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, node, plan, sqltype.Assignment, false}, nil
}

type jsonPredicateExpression struct {
	expressionInfo
	child       BoundExpr
	kind        string
	unique, not bool
}

func (e jsonPredicateExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.usage.Type), nil
	}
	truth, err := sqltype.JSONPredicate(c.Context, value, strings.ToUpper(e.kind), e.unique)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.not {
		truth = !truth
	}
	return sqltype.Boolean(e.usage.Type, truth), nil
}
func (b *expressionBinder) bindJSONPredicate(raw parser.JSONPredicateExpr) (BoundExpr, error) {
	operand, err := b.comparisonOperand(raw.Child)
	if err != nil {
		return nil, err
	}
	child := operand.node
	if operand.descriptor() == nil {
		child, err = b.finalizeOperand(operand, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
	}
	typ := child.ResultUsage().Type
	if typ.ID() != sqltype.JSON && typ.ID() != sqltype.JSONB && typ.ID() != sqltype.Bytea && !sqltype.StringCategory(typ.ID()) {
		return nil, sqlError("42804", "cannot use type "+b.diagnosticTypeName(typ)+" in IS JSON predicate")
	}
	return jsonPredicateExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, child.Properties()), child: child, kind: raw.Kind, unique: raw.Unique, not: raw.Not}, nil
}
