package engine

import (
	"fmt"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type transactionTimeKind uint8

const (
	transactionTimeNow transactionTimeKind = iota + 1
	transactionTimeTransaction
	transactionTimeStatement
	transactionTimeClock
	transactionTimeOfDay
)

func transactionTimeName(kind transactionTimeKind) string {
	switch kind {
	case transactionTimeNow:
		return "now"
	case transactionTimeTransaction:
		return "transaction_timestamp"
	case transactionTimeStatement:
		return "statement_timestamp"
	case transactionTimeClock:
		return "clock_timestamp"
	case transactionTimeOfDay:
		return "timeofday"
	}
	return ""
}
func transactionTimeKindForName(name string) transactionTimeKind {
	switch name {
	case "now":
		return transactionTimeNow
	case "transaction_timestamp":
		return transactionTimeTransaction
	case "statement_timestamp":
		return transactionTimeStatement
	case "clock_timestamp":
		return transactionTimeClock
	case "timeofday":
		return transactionTimeOfDay
	}
	return 0
}
func transactionTimeProperties(kind transactionTimeKind) ExprProperties {
	if kind == transactionTimeClock || kind == transactionTimeOfDay {
		return ExprProperties{Volatility: Volatile}
	}
	return ExprProperties{Volatility: Stable}
}
func transactionTimeType(kind transactionTimeKind) sqltype.ID {
	if kind == transactionTimeOfDay {
		return sqltype.Text
	}
	return sqltype.Timestamptz
}

type transactionTimeExpression struct {
	expressionInfo
	kind transactionTimeKind
}

func validateTransactionTime(e transactionTimeExpression) error {
	if transactionTimeName(e.kind) == "" || e.usage.Type == nil || !e.usage.Valid() || e.usage.Type.ID() != transactionTimeType(e.kind) || e.usage.Typmod != -1 || e.Properties() != transactionTimeProperties(e.kind) {
		return fmt.Errorf("stuntgres: invalid transaction time expression")
	}
	return nil
}
func (e transactionTimeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := validateTransactionTime(e); err != nil {
		return sqltype.Value{}, err
	}
	stamp := c.Input.TransactionStart
	switch e.kind {
	case transactionTimeStatement:
		if c.liveScope() == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement time scope")
		}
		stamp = c.liveScope().statementStart
		if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
			stamp = c.query.source.authority.expression.statementStart
		}
	case transactionTimeClock, transactionTimeOfDay:
		stamp = time.Now().UTC().Truncate(time.Microsecond)
	}
	if e.kind == transactionTimeOfDay {
		return sqltype.String(e.usage.Type, transactionTimeOfDayText(stamp, c.Input.Zone)), nil
	}
	input := c.Input
	input.TransactionStart = stamp
	return sqltype.CurrentDateTime(e.usage, input)
}

// OffsetAt owns both the civil offset and abbreviation. In particular, a POSIX
// offset zone may have an empty abbreviation, leaving the final space intact.
func transactionTimeOfDayText(stamp time.Time, zone *sqltype.Zone) string {
	east, abbreviation := zone.OffsetAt(stamp.Unix())
	local := stamp.UTC().Add(time.Duration(east) * time.Second)
	var buffer [96]byte
	text := local.AppendFormat(buffer[:0], "Mon Jan 02 15:04:05.000000 2006 ")
	text = append(text, abbreviation...)
	return string(text)
}
func (e transactionTimeExpression) sql() (string, error) {
	if err := validateTransactionTime(e); err != nil {
		return "", err
	}
	return transactionTimeName(e.kind) + "()", nil
}
func (b *expressionBinder) bindTransactionTime(raw parser.FunctionExpr) (BoundExpr, error) {
	kind := transactionTimeKindForName(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, err = b.functionArgumentTypeNames(raw)
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
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return nil, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || kind == 0 || len(raw.Args) != 0 || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := transactionTimeName(kind)
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	return transactionTimeExpression{expressionInfo: b.info(b.typ(transactionTimeType(kind)).Usage(), raw.Pos, transactionTimeProperties(kind)), kind: kind}, nil
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
	case transactionTimeExpression:
		return validateTransactionTime(n) == nil
	case similarExpression, similarEscapeExpression, similarSubstringExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case enumFunctionExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, likeEscapeExpression, nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, binaryExpression, datetimeJustifyExpression, datetimeAgeFiniteExpression, datetimeMakeExpression, datetimeFormattingExpression, numericFormattingExpression, stringExpression, timeZoneExpression, mathExpression, nullCountExpression, formatExpression, caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
