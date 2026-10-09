package parser

import (
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
)

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
