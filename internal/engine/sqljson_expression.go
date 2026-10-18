package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type sqlJSONExpression struct {
	expr.Info
	raw  parser.SQLJSONExpr
	args []expr.Expr
	plan sqltype.SQLJSONPlan
}

func (e sqlJSONExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
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
func (e sqlJSONExpression) reconstruct(info expr.Info, args []expr.Expr) (expr.Expr, error) {
	var small [8]sqltype.Usage
	usages := small[:min(len(args), len(small))]
	if len(args) > len(small) {
		usages = make([]sqltype.Usage, len(args))
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindSQLJSON(sqlJSONKind(e.raw.Kind), info.Usage, usages, e.raw.Absent, e.raw.Unique && e.raw.Kind != "json")
	if err != nil {
		return nil, err
	}
	e.Info, e.args, e.plan = info, args, plan
	return e, nil
}

// sqlJSONInput applies a constructor argument's FORMAT JSON clause, or the
// implicit JSON input of JSON() and JSON_SERIALIZE(): JSON and JSONB pass
// through, string and bytea inputs convert explicitly to JSON.
func sqlJSONInput(b functions.Binder, arg expr.Expr, format *parser.JSONFormat, implicit, unique bool) (expr.Expr, error) {
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
		return nil, expr.ErrorAt("42804", "JSON ENCODING clause is only allowed for bytea input type", pos)
	}
	if base.ID() == sqltype.JSON || base.ID() == sqltype.JSONB {
		return arg, nil
	}
	if base.ID() != sqltype.Bytea && !sqltype.StringCategory(base.ID()) {
		if format != nil {
			return nil, expr.ErrorAt("42804", "cannot use non-string types with explicit FORMAT JSON clause", pos)
		}
		return nil, expr.ErrorAt("42846", "cannot cast type "+base.DiagnosticName()+" to json", arg.SourcePosition())
	}
	target := b.Type(sqltype.JSON).Usage()
	plan, err := sqltype.PlanJSONFormattedInput(arg.ResultUsage(), target, encoding)
	if err != nil {
		return nil, err
	}
	if unique {
		plan, err = sqltype.PlanJSONUniqueInput(arg.ResultUsage(), target, encoding)
		if err != nil {
			return nil, err
		}
	}
	return b.CoerceArgument(functions.Argument{Expression: arg, Usage: arg.ResultUsage()}, functions.CoercionRequest{Intent: functions.JSONFormattedArgument, Target: target, Plan: plan, Planned: true})
}

// sqlJSONReturningType resolves a SQL/JSON RETURNING clause, rejecting SETOF
// and pseudo-types.
func sqlJSONReturningType(b functions.Binder, raw parser.TypeName) (sqltype.Usage, error) {
	if raw.Setof {
		return sqltype.Usage{}, sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions")
	}
	returning, err := b.JSONReturningType(raw)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if functions.JSONReturningPseudoType(returning.Type) {
		return sqltype.Usage{}, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
	}
	return returning, nil
}

// bindSQLJSON binds a SQL/JSON constructor. The query form of JSON_ARRAY is a
// subquery; the other forms bind as a constructor whose RETURNING clause the
// engine applies as an assignment coercion.
func (b *expressionBinder) bindSQLJSON(raw parser.SQLJSONExpr) (expr.Expr, error) {
	if sqlJSONKind(raw.Kind) == 0 {
		return nil, fmt.Errorf("invalid SQL/JSON constructor")
	}
	if raw.Query != nil {
		return b.bindSQLJSONArrayQuery(raw)
	}
	node, returning, err := bindSQLJSONConstructor(b, raw)
	if err != nil {
		return nil, err
	}
	if returning.Type == nil {
		return node, nil
	}
	if err = b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	plan, err := functions.JSONReturningCoercion(node.ResultUsage(), returning, raw.Output)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, coercionProperties(node, plan)), node)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, node, plan, sqltype.Assignment, false}, nil
}

// bindSQLJSONConstructor binds a constructor without its query form and
// returns its RETURNING usage (zero when absent) for the caller to apply.
func bindSQLJSONConstructor(b functions.Binder, raw parser.SQLJSONExpr) (expr.Expr, sqltype.Usage, error) {
	kind := sqlJSONKind(raw.Kind)
	if kind >= sqltype.SQLJSONParse && len(raw.Args) != 1 || kind == sqltype.SQLJSONObject && len(raw.Args)%2 != 0 {
		return nil, sqltype.Usage{}, expr.ErrorAt("42601", "invalid SQL/JSON constructor arguments", raw.Pos)
	}
	if kind == sqltype.SQLJSONScalar && raw.Returning != nil {
		return nil, sqltype.Usage{}, expr.ErrorAt("42601", "syntax error at or near \"RETURNING\"", raw.Returning.Pos)
	}
	var returning sqltype.Usage
	var err error
	if raw.Returning != nil {
		returning, err = sqlJSONReturningType(b, *raw.Returning)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
	}
	args := make([]expr.Expr, len(raw.Args))
	properties := expr.Properties{Volatility: expr.Immutable}
	id := sqltype.JSON
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		arg := operand.Expression
		if kind == sqltype.SQLJSONObject && i%2 == 0 && b.RetainUntypedParameter(arg) {
			args[i] = arg
			properties = expr.MergeProperties(properties, arg.Properties())
			continue
		}
		if operand.Usage.Type == nil {
			arg, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
			if err != nil {
				return nil, sqltype.Usage{}, err
			}
		}
		if raw.Unique && kind == sqltype.SQLJSONParse && arg.ResultUsage().Type.BaseType().ID() != sqltype.Bytea && !sqltype.StringCategory(arg.ResultUsage().Type.BaseType().ID()) {
			return nil, sqltype.Usage{}, expr.ErrorAt("42804", "cannot use non-string types with WITH UNIQUE KEYS clause", raw.Pos)
		}
		var format *parser.JSONFormat
		if i < len(raw.Formats) {
			format = raw.Formats[i]
		}
		arg, err = sqlJSONInput(b, arg, format, kind == sqltype.SQLJSONParse || kind == sqltype.SQLJSONSerialize, raw.Unique && kind == sqltype.SQLJSONParse)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		if arg.ResultUsage().Type.ID() == sqltype.JSONB && !(kind == sqltype.SQLJSONObject && i%2 == 0) {
			id = sqltype.JSONB
		}
		args[i] = arg
		if arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
		properties = expr.MergeProperties(properties, arg.Properties())
		base := arg.ResultUsage().Type.BaseType()
		if kind == sqltype.SQLJSONScalar || kind == sqltype.SQLJSONArray || kind == sqltype.SQLJSONObject {
			if base.IsComposite() || base.OutputSessionDependent() || base.ID() == sqltype.Date || base.ID() == sqltype.Timestamp || base.ID() == sqltype.Timestamptz {
				properties.Volatility = max(properties.Volatility, expr.Stable)
			}
		}
	}
	if err := functions.ValidateJSONOutputFormat(returning, raw.Output); err != nil {
		return nil, sqltype.Usage{}, err
	}
	if kind == sqltype.SQLJSONSerialize {
		if returning.Type == nil {
			returning = b.Type(sqltype.Text).Usage()
		}
		base := returning.Type.BaseType()
		if base.ID() != sqltype.Bytea && !sqltype.StringCategory(base.ID()) {
			d := sqlError("42804", "cannot use type "+returning.Type.DiagnosticName()+" in RETURNING clause of JSON_SERIALIZE()").(*sqlstate.Error)
			d.Hint = "Try returning a string type or bytea."
			return nil, sqltype.Usage{}, d
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
	node, err := (sqlJSONExpression{raw: raw}).reconstruct(b.Info(b.Type(id).Usage(), raw.Pos, properties), args)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	return node, returning, nil
}

type jsonPredicateExpression struct {
	expr.Info
	child       expr.Expr
	kind        string
	unique, not bool
}

func (e jsonPredicateExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	truth, err := sqltype.JSONPredicate(c.Context, value, strings.ToUpper(e.kind), e.unique)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.not {
		truth = !truth
	}
	return sqltype.Boolean(e.Usage.Type, truth), nil
}
func bindJSONPredicate(b functions.Binder, raw parser.JSONPredicateExpr) (expr.Expr, error) {
	operand, err := b.AnalyzeArgument(raw.Child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	child := operand.Expression
	if operand.Usage.Type == nil {
		child, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	typ := child.ResultUsage().Type
	if typ.ID() != sqltype.JSON && typ.ID() != sqltype.JSONB && typ.ID() != sqltype.Bytea && !sqltype.StringCategory(typ.ID()) {
		return nil, sqlError("42804", "cannot use type "+b.DiagnosticTypeName(typ)+" in IS JSON predicate")
	}
	return jsonPredicateExpression{Info: b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, child.Properties()), child: child, kind: raw.Kind, unique: raw.Unique, not: raw.Not}, nil
}
