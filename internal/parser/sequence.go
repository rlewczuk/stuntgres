package parser

import (
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
)

// Relationless SELECT is bounded to the selected sequence builtins and explicit
// regclass expressions, not arbitrary scalar projections or general routines.
func sequenceProjection(s *Select) bool {
	var contains func(Expr) bool
	contains = func(e Expr) bool {
		switch n := e.(type) {
		case CaseExpr:
			if contains(n.Test) || contains(n.Else) {
				return true
			}
			for _, arm := range n.Arms {
				if contains(arm.Condition) || contains(arm.Result) {
					return true
				}
			}
		case ConditionalExpr:
			for _, arg := range n.Args {
				if contains(arg) {
					return true
				}
			}
		case CallExpr:
			return true
		case RegclassExpr:
			return true
		case CollateExpr:
			return contains(n.Child)
		case CastExpr:
			return contains(n.Child)
		case TimeZoneExpr:
			return contains(n.Value) || !n.Local && contains(n.Zone)
		case FunctionExpr:
			for _, arg := range n.Args {
				if contains(arg) {
					return true
				}
			}
		case PredicateExpr:
			return contains(n.Left) || contains(n.Right)
		case OperatorExpr:
			for _, a := range n.Args {
				if contains(a) {
					return true
				}
			}
		case LogicalExpr:
			return contains(n.Left) || contains(n.Right)
		}
		return false
	}
	for _, e := range s.Targets {
		if contains(e) {
			return true
		}
	}
	return false
}

func sequenceInteger(l sqlLexer, tok lexeme, sign string) (int64, bool) {
	v, ok := integerValue(l, tok, sign)
	if !ok {
		return 0, false
	}
	if v.Kind == IntegerValue {
		return v.Integer, true
	}
	n, err := strconv.ParseInt(v.Text, 10, 64)
	if err == nil {
		return n, true
	}
	code, message := "22P02", fmt.Sprintf("invalid input syntax for type bigint: %q", v.Text)
	if num, ok := err.(*strconv.NumError); ok && num.Err == strconv.ErrRange {
		code, message = "22003", fmt.Sprintf("value %q is out of range for type bigint", v.Text)
	}
	l.(*lexer).err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	return 0, false
}
