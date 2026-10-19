package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"time"
)

type TransactionTimeKind uint8

const (
	TransactionTimeNow TransactionTimeKind = iota + 1
	TransactionTimeTransaction
	TransactionTimeStatement
	TransactionTimeClock
	TransactionTimeOfDay
)

func (kind TransactionTimeKind) Name() string {
	switch kind {
	case TransactionTimeNow:
		return "now"
	case TransactionTimeTransaction:
		return "transaction_timestamp"
	case TransactionTimeStatement:
		return "statement_timestamp"
	case TransactionTimeClock:
		return "clock_timestamp"
	case TransactionTimeOfDay:
		return "timeofday"
	}
	return ""
}
func TransactionTimeKindForName(name string) TransactionTimeKind {
	switch name {
	case "now":
		return TransactionTimeNow
	case "transaction_timestamp":
		return TransactionTimeTransaction
	case "statement_timestamp":
		return TransactionTimeStatement
	case "clock_timestamp":
		return TransactionTimeClock
	case "timeofday":
		return TransactionTimeOfDay
	}
	return 0
}
func (kind TransactionTimeKind) IntrinsicRule() expr.IntrinsicPropertyRule {
	if kind == TransactionTimeClock || kind == TransactionTimeOfDay {
		return expr.ExactVolatile
	}
	return expr.ExactStable
}
func (kind TransactionTimeKind) Type() sqltype.ID {
	if kind == TransactionTimeOfDay {
		return sqltype.Text
	}
	return sqltype.Timestamptz
}

type TransactionTime struct {
	expr.Info
	Kind TransactionTimeKind
}

func (e TransactionTime) Validate(intrinsicMatch bool) error {
	if e.Kind.Name() == "" || e.Usage.Type == nil || !e.Usage.Valid() || e.Usage.Type.ID() != e.Kind.Type() || e.Usage.Typmod != -1 || !intrinsicMatch {
		return fmt.Errorf("stuntgres: invalid transaction time expression")
	}
	return nil
}

// transactionTimeSQL validates the leaf and spells its call.
func transactionTimeSQL(e TransactionTime) (string, error) {
	if err := e.Validate(expr.PropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return "", err
	}
	return e.Kind.Name() + "()", nil
}
func (e TransactionTime) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := e.Validate(c.FunctionPropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return sqltype.Value{}, err
	}
	if e.Kind == TransactionTimeOfDay {
		return sqltype.String(e.Usage.Type, transactionTimeOfDayText(c.FunctionWallTime(), c.FunctionTimeOfDayZone())), nil
	}
	input := c.FunctionInput()
	stamp := input.TransactionStart
	switch e.Kind {
	case TransactionTimeStatement:
		var err error
		stamp, err = c.FunctionStatementTime()
		if err != nil {
			return sqltype.Value{}, err
		}
	case TransactionTimeClock:
		stamp = c.FunctionWallTime()
	}
	input.TransactionStart = stamp
	return sqltype.CurrentDateTime(e.Usage, input)
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
func TransactionTimeAllowed(raw parser.FunctionExpr, visible bool, kind TransactionTimeKind) bool {
	return visible && kind != 0 && len(raw.Args) == 0 && !raw.Variadic && len(raw.ArgNames) == 0
}
func TransactionTimeDecoration(raw parser.FunctionExpr) string {
	switch {
	case raw.Star:
		return "(*)"
	case raw.Distinct:
		return "DISTINCT"
	case len(raw.Order) != 0:
		return "ORDER BY"
	case raw.Filter != nil:
		return "FILTER"
	case raw.Window != nil:
		return "OVER"
	}
	return ""
}

// TransactionTime implements expr.Composite as a leaf: no children, and Rebuild only takes
// the new metadata.
var _ expr.Composite = TransactionTime{}

func (e TransactionTime) Children() []expr.Expr { return nil }
func (e TransactionTime) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("TransactionTime", children, 0); err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}

// IntrinsicVolatility is the analyzed volatility: the call is volatile or
// has no children, so nothing from eliminated arms can be retained.
func (e TransactionTime) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }

func (e TransactionTime) PlanningFold() expr.Fold { return expr.FoldNever }

var _ expr.Renderable = TransactionTime{}

// Render implements expr.Renderable.
func (e TransactionTime) Render() (expr.Rendering, error) {
	text, err := transactionTimeSQL(e)
	if err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: fmt.Sprintf("TransactionTime kind %d", e.Kind), Form: expr.FormLeaf, Text: text}, nil
}

// SameGroup implements expr.GroupIdentity.
func (e TransactionTime) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(TransactionTime)
	return ok && e.Kind == o.Kind
}

// GroupLeaf implements expr.GroupLeaf.
func (e TransactionTime) GroupLeaf() {}

// PreflightFold implements expr.Preflighting.
func (e TransactionTime) PreflightFold() expr.Preflight { return expr.PreflightKeep }
