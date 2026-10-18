package engine

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/expr"
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
func transactionTimeSQL(e functions.TransactionTime) (string, error) {
	if err := e.Validate(expr.PropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
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
	info := b.info(b.typ(kind.Type()).Usage(), raw.Pos, expr.IntrinsicProperties(kind.IntrinsicRule()))
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return functions.TransactionTime{Info: functionInfo(info), Kind: kind}, nil
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
	case functions.TransactionTime:
		return n.Validate(expr.PropertiesMatch(n.Property, n.Kind.IntrinsicRule())) == nil
	case functions.DatetimeSleep:
		return n.Validate(expr.PropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) && closedTransactionTimeDefault(n.Arg)
	case rowExpression, fieldExpression, functions.Posix, functions.PosixSubstring, functions.Similar, functions.SimilarEscape, functions.SimilarSubstring, functions.Regexp:
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
	case catalogUserNameExpression:
		return closedTransactionTimeDefault(n.arg)
	case catalogForeignKeysExpression:
		return false
	case transactionIdentityExpression:
		return n.arg == nil || closedTransactionTimeDefault(n.arg)
	case commitTimestampExpression:
		return n.arg == nil || closedTransactionTimeDefault(n.arg)
	case functions.UUIDExtract:
		return closedTransactionTimeDefault(n.Arg)
	case functions.NetworkFormat:
		for _, arg := range n.Args[:n.Kind.Arity()] {
			if !closedTransactionTimeDefault(arg) {
				return false
			}
		}
		return true
	case functions.ArrayRandom:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case functions.ArrayTransform, functions.RangeOperator, functions.RangeInspect, functions.Enum, functions.ArraySearch, functions.ArrayInspect:
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
	case functions.JSONBPathExists, functions.JSONBPathMatch, functions.JSONBPathQueryFirst, functions.JSONBPathQueryArray, functions.JSONBSet, functions.JSONStripNulls, functions.JSONExtract:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, functions.LikeEscape, nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, functions.Binary, functions.DatetimeJustify, functions.DatetimeAgeFinite, functions.DatetimeMake, functions.DatetimeFormatting, functions.NumericFormatting, functions.String, functions.Timezone, functions.Math, functions.NullCount, functions.Format, functions.StringVariadic, functions.JSONCreate, caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
