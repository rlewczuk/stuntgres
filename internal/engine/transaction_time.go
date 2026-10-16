package engine

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionStatementTime() (time.Time, error) {
	scope := c.liveScope()
	if scope == nil {
		return time.Time{}, fmt.Errorf("stuntgres: missing statement time scope")
	}
	stamp := scope.statementStart
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		stamp = c.query.source.authority.expression.statementStart
	}
	return stamp, nil
}
func (c *EvalContext) FunctionWallTime() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
func (c *EvalContext) FunctionTimeOfDayZone() *sqltype.Zone {
	return c.Input.Zone
}
func transactionTimeSQL(e functions.TransactionTime[*EvalContext, ExprProperties]) (string, error) {
	if err := e.Validate(functionPropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return "", err
	}
	return e.Kind.Name() + "()", nil
}
func (b *expressionBinder) bindTransactionTime(raw parser.FunctionExpr) (BoundExpr, error) {
	kind := functions.TransactionTimeKindForName(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, _, err = b.functionArgumentTypeNames(raw)
		if err != nil {
			return nil, err
		}
	}
	if raw.Filter != nil {
		if _, err := b.bindBoolean(raw.Filter, projectionContext, "FILTER"); err != nil {
			return nil, err
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return nil, fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !functions.TransactionTimeAllowed(raw, visible, kind) {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	if decoration := functions.TransactionTimeDecoration(raw); decoration != "" {
		if decoration == "(*)" {
			return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
		}
		owner := "an aggregate function"
		if decoration == "OVER" {
			owner = "a window function nor an aggregate function"
		}
		return nil, expressionError("42809", decoration+" specified, but "+name+" is not "+owner, raw.Pos)
	}
	info := b.info(b.typ(kind.Type()).Usage(), raw.Pos, functionIntrinsicProperties(kind.IntrinsicRule()))
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return functions.TransactionTime[*EvalContext, ExprProperties]{Info: functionInfo(info), Kind: kind}, nil
}

// Only selected time leaves may introduce volatility into this closed default.
// Other volatile producers retain their separately owned admission rules.
func closedTransactionTimeDefault(node BoundExpr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case functions.TransactionTime[*EvalContext, ExprProperties]:
		return n.Validate(functionPropertiesMatch(n.Property, n.Kind.IntrinsicRule())) == nil
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		return n.Validate(functionPropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) && closedTransactionTimeDefault(n.Arg)
	case rowExpression, fieldExpression, functions.Posix[BoundExpr, *EvalContext, ExprProperties], functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties], functions.Similar[BoundExpr, *EvalContext, ExprProperties], functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties], functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties], functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case aclValueExpression:
		if n.kind == aclExplode {
			return false
		}
		for _, child := range n.arguments() {
			if !closedTransactionTimeDefault(child) {
				return false
			}
		}
		return true
	case commitTimestampExpression:
		return n.arg == nil || closedTransactionTimeDefault(n.arg)
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		return closedTransactionTimeDefault(n.Arg)
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties], functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties], functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties], functions.Enum[BoundExpr, *EvalContext, ExprProperties], functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties], functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case scalarArrayComparisonExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case sqlJSONExpression, jsonPredicateExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties], functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties], functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties], functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties], nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, functions.Binary[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties], functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties], functions.String[BoundExpr, *EvalContext, ExprProperties], functions.Timezone[BoundExpr, *EvalContext, ExprProperties], functions.Math[BoundExpr, *EvalContext, ExprProperties], functions.NullCount[BoundExpr, *EvalContext, ExprProperties], functions.Format[BoundExpr, *EvalContext, ExprProperties], functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin], functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties], caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
