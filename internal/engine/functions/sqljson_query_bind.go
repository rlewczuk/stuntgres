package functions

import (
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func BindSQLJSONQuery(b Binder, raw parser.SQLJSONQueryExpr) (expr.Expr, error) {
	result := b.Type(sqltype.Text).Usage()
	if raw.Kind == "json_exists" {
		result = b.Type(sqltype.Bool).Usage()
	}
	if raw.Kind == "json_query" {
		result = b.Type(sqltype.JSONB).Usage()
	}
	if raw.Returning != nil {
		var err error
		result, err = SQLJSONReturningType(b, *raw.Returning)
		if err != nil {
			return nil, err
		}
	}
	if raw.Kind == "json_value" && raw.Output != nil {
		return nil, expr.ErrorAt("42601", "cannot specify FORMAT JSON in RETURNING clause of JSON_VALUE()", raw.Output.Pos)
	}
	if err := ValidateJSONOutputFormat(result, raw.Output); err != nil {
		return nil, err
	}
	if raw.Quotes != "" && (raw.Quotes == "omit" || raw.TableColumn != "") && raw.Wrapper != "" && raw.Wrapper != "without" {
		return nil, expr.ErrorAt("42601", "SQL/JSON QUOTES behavior must not be specified when WITH WRAPPER is used", raw.Pos)
	}
	for i, behavior := range []parser.SQLJSONBehavior{raw.Empty, raw.Error} {
		if behavior.Kind == "" {
			continue
		}
		allowed := "ERROR, NULL, EMPTY ARRAY, EMPTY OBJECT, or DEFAULT expression"
		valid := behavior.Kind == "error" || behavior.Kind == "null" || behavior.Kind == "default" || behavior.Kind == "empty array" || behavior.Kind == "empty object"
		if raw.Kind == "json_exists" {
			allowed = "ERROR, TRUE, FALSE, or UNKNOWN"
			valid = behavior.Kind == "error" || behavior.Kind == "true" || behavior.Kind == "false" || behavior.Kind == "unknown"
		}
		if raw.Kind == "json_value" {
			allowed = "ERROR, NULL, or DEFAULT expression"
			valid = behavior.Kind == "error" || behavior.Kind == "null" || behavior.Kind == "default"
		}
		if !valid {
			branch := "ERROR"
			if i == 0 {
				branch = "EMPTY"
			}
			diagnostic := expr.ErrorAt("42601", "invalid ON "+branch+" behavior", behavior.Pos).(*sqlstate.Error)
			diagnostic.Detail = "Only " + allowed + " is allowed in ON " + branch + " for " + strings.ToUpper(raw.Kind) + "()."
			if raw.TableColumn != "" {
				family := "formatted"
				if raw.Kind == "json_value" {
					family = "scalar"
				}
				if raw.Kind == "json_exists" {
					family = "EXISTS"
				}
				diagnostic.Message = "invalid ON " + branch + " behavior for column \"" + raw.TableColumn + "\""
				diagnostic.Detail = "Only " + allowed + " is allowed in ON " + branch + " for " + family + " columns."
			}
			return nil, diagnostic
		}
	}
	document, err := sqlJSONQuerySource(b, raw.Document, sqltype.JSONB, raw.Input)
	if err != nil {
		return nil, err
	}
	path, err := sqlJSONQuerySource(b, raw.Path, sqltype.JSONPath, nil)
	if err != nil {
		return nil, err
	}
	args := []expr.Expr{document, path}
	properties := expr.MergeProperties(document.Properties(), path.Properties())
	names := make([]string, len(raw.Passing))
	usages := make([]sqltype.Usage, len(raw.Passing))
	for i, binding := range raw.Passing {
		operand, err := b.AnalyzeArgument(binding.Value, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arg := operand.Expression
		if operand.Usage.Type == nil {
			arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
			if err != nil {
				return nil, err
			}
		}
		if err := b.CaptureTypeInput(arg.ResultUsage().Type); err != nil {
			return nil, err
		}
		args = append(args, arg)
		names[i] = binding.Name.Text
		usages[i] = arg.ResultUsage()
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if !b.LiteralNull(path) {
		if constant, ok := b.ConstantValue(path); !ok || sqltype.SQLJSONPathMutable(constant, names, usages) {
			properties.Volatility = max(properties.Volatility, expr.Stable)
		}
	}
	for _, behavior := range []parser.SQLJSONBehavior{raw.Empty, raw.Error} {
		if behavior.Default == nil {
			continue
		}
		if err := sqlJSONDefaultShape(behavior.Default, raw.TableColumn != ""); err != nil {
			return nil, err
		}
		operand, err := b.AnalyzeArgument(behavior.Default, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arg := operand.Expression
		if err := b.ValidateSQLJSONDefault(behavior.Default, arg); err != nil {
			return nil, err
		}
		if sqlJSONDefaultSet(arg) {
			return nil, expr.ErrorAt("42804", "DEFAULT expression must not return a set", behavior.Default.SourcePosition())
		}
		if operand.Usage.Type == nil {
			arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: result.BaseUsage()})
			if err != nil {
				return nil, err
			}
		}
		if arg.Properties().RowDependent || arg.Properties().StageDependent {
			return nil, expr.ErrorAt("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", behavior.Default.SourcePosition())
		}
		args = append(args, arg)
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if result.Type.HasDomainConstraints() {
		properties.RuntimeDomainConversion = true
	}
	if err := b.CaptureTypeInput(result.Type); err != nil {
		return nil, err
	}
	return NewSQLJSONQuery(b.Info(result, raw.Pos, properties), raw, args, b.Type(sqltype.JSONB).Usage())
}

func sqlJSONQuerySource(b Binder, raw parser.Expr, id sqltype.ID, format *parser.JSONFormat) (expr.Expr, error) {
	operand, err := b.AnalyzeArgument(raw, AnalysisRequest{Intent: ComparisonArgument})
	if err != nil {
		return nil, err
	}
	target := b.Type(id).Usage()
	if operand.Usage.Type == nil && format == nil {
		literal, isLiteral := raw.(parser.LiteralExpr)
		if id != sqltype.JSONB || !isLiteral || literal.Value.Kind != parser.StringValue {
			return b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
		}
		// An unformatted unknown document is text input at execution, not
		// a JSON literal to parse during analysis or constant preflight.
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
		return newSQLJSONDocument(b.Info(target, raw.SourcePosition(), arg.Properties()), arg)
	}
	arg := operand.Expression
	if operand.Usage.Type == nil {
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	if format != nil {
		arg, err = SQLJSONInput(b, arg, format, true, false)
		if err != nil {
			return nil, err
		}
	}
	usage := arg.ResultUsage()
	base := usage.Type.BaseType()
	if id == sqltype.JSONPath && base.ID() != sqltype.JSONPath && !sqltype.StringCategory(base.ID()) {
		return nil, expr.ErrorAt("42804", "JSON path expression must be of type jsonpath, not of type "+b.DiagnosticTypeName(usage.Type), raw.SourcePosition())
	}
	plan, err := sqltype.PlanCoercion(usage, target, sqltype.Explicit)
	if err != nil {
		return nil, err
	}
	if usage == target {
		return arg, nil
	}
	return b.CoerceArgument(Argument{Expression: arg, Usage: usage}, CoercionRequest{Intent: JSONFormattedArgument, Target: target, Plan: plan, Planned: true})
}

func sqlJSONDefaultShape(raw parser.Expr, tableColumn bool) error {
	return parser.VisitQueryExpressions(&parser.Select{Targets: []parser.Expr{raw}}, func(child parser.Expr) error {
		invalid := false
		switch r := child.(type) {
		case parser.ColumnExpr:
			// JSON_TABLE binds ordinary scalar names before distinguishing
			// enclosing shape from references in the complete bound tree.
			invalid = !tableColumn
		case parser.SubqueryExpr, parser.AggregateExpr, parser.GroupingExpr:
			invalid = true
		case parser.FunctionExpr:
			invalid = r.Window != nil
		}
		if invalid {
			return expr.ErrorAt("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", raw.SourcePosition())
		}
		return nil
	})
}

func sqlJSONDefaultSet(node expr.Expr) bool {
	if source, ok := node.(expr.Source); ok {
		_, set := source.SourceAdmission()
		if set {
			return true
		}
	}
	if composite, ok := node.(expr.Composite); ok {
		for _, child := range composite.Children() {
			if child != nil && sqlJSONDefaultSet(child) {
				return true
			}
		}
	}
	return false
}
