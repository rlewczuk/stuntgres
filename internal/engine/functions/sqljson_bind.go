package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SQLJSONInput applies a constructor argument's FORMAT JSON clause, or the
// implicit JSON input of JSON() and JSON_SERIALIZE(): JSON and JSONB pass
// through, string and bytea inputs convert explicitly to JSON.
func SQLJSONInput(b Binder, arg expr.Expr, format *parser.JSONFormat, implicit, unique bool) (expr.Expr, error) {
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
	return b.CoerceArgument(Argument{Expression: arg, Usage: arg.ResultUsage()}, CoercionRequest{Intent: JSONFormattedArgument, Target: target, Plan: plan, Planned: true})
}

// SQLJSONReturningType resolves a SQL/JSON RETURNING clause, rejecting SETOF
// and pseudo-types.
func SQLJSONReturningType(b Binder, raw parser.TypeName) (sqltype.Usage, error) {
	if raw.Setof {
		return sqltype.Usage{}, sqlError("0A000", "returning SETOF types is not supported in SQL/JSON functions")
	}
	returning, err := b.JSONReturningType(raw)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if JSONReturningPseudoType(returning.Type) {
		return sqltype.Usage{}, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
	}
	return returning, nil
}

// BindSQLJSON binds a constructor without its query form and
// returns its RETURNING usage (zero when absent) for the caller to apply.
func BindSQLJSON(b Binder, raw parser.SQLJSONExpr) (expr.Expr, sqltype.Usage, error) {
	kind := SQLJSONKind(raw.Kind)
	if kind >= sqltype.SQLJSONParse && len(raw.Args) != 1 || kind == sqltype.SQLJSONObject && len(raw.Args)%2 != 0 {
		return nil, sqltype.Usage{}, expr.ErrorAt("42601", "invalid SQL/JSON constructor arguments", raw.Pos)
	}
	if kind == sqltype.SQLJSONScalar && raw.Returning != nil {
		return nil, sqltype.Usage{}, expr.ErrorAt("42601", "syntax error at or near \"RETURNING\"", raw.Returning.Pos)
	}
	var returning sqltype.Usage
	var err error
	if raw.Returning != nil {
		returning, err = SQLJSONReturningType(b, *raw.Returning)
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
	}
	args := make([]expr.Expr, len(raw.Args))
	properties := expr.Properties{Volatility: expr.Immutable}
	id := sqltype.JSON
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
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
			arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
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
		arg, err = SQLJSONInput(b, arg, format, kind == sqltype.SQLJSONParse || kind == sqltype.SQLJSONSerialize, raw.Unique && kind == sqltype.SQLJSONParse)
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
	if err := ValidateJSONOutputFormat(returning, raw.Output); err != nil {
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
	node, err := NewSQLJSON(b.Info(b.Type(id).Usage(), raw.Pos, properties), raw, args)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	return node, returning, nil
}

// BindJSONPredicate binds IS JSON over a JSON, JSONB, bytea or string operand.
func BindJSONPredicate(b Binder, raw parser.JSONPredicateExpr) (expr.Expr, error) {
	operand, err := b.AnalyzeArgument(raw.Child, AnalysisRequest{Intent: ComparisonArgument})
	if err != nil {
		return nil, err
	}
	child := operand.Expression
	if operand.Usage.Type == nil {
		child, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	typ := child.ResultUsage().Type
	if typ.ID() != sqltype.JSON && typ.ID() != sqltype.JSONB && typ.ID() != sqltype.Bytea && !sqltype.StringCategory(typ.ID()) {
		return nil, sqlError("42804", "cannot use type "+b.DiagnosticTypeName(typ)+" in IS JSON predicate")
	}
	return JSONPredicate{Info: b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, child.Properties()), Child: child, Kind: raw.Kind, Unique: raw.Unique, Not: raw.Not}, nil
}
