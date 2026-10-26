package proc

import (
	"reflect"
	"strings"
	"stuntgres/internal/parser"
	"unicode/utf8"
)

func validPLSQLSource(source string, position parser.Position, q *parser.PLSQLStatement) bool {
	if !parser.ValidPLSQLStatement(q) || position != q.Start || q.Start.Offset < 0 || q.Start.Offset >= q.End.Offset || q.End != q.Delimiter || q.End.Offset >= len(source) || source[q.End.Offset] != ';' || q.Text != source[q.Start.Offset:q.End.Offset] {
		return false
	}
	syntax := reflect.ValueOf(q.Query)
	if syntax.Kind() == reflect.Pointer && syntax.IsNil() {
		return false
	}
	if at, ok := plQueryLoopPosition(q.Query); ok && (at.Offset < q.Start.Offset || at.Offset >= q.End.Offset || at.Character != utf8.RuneCountInString(source[:at.Offset])+1) {
		return false
	}
	if call, ok := q.Query.(*parser.Call); ok && call.Pos != q.Start {
		return false
	}
	for _, at := range []parser.Position{q.Start, q.End} {
		if at.Character != utf8.RuneCountInString(source[:at.Offset])+1 {
			return false
		}
	}
	perform := len(q.Text) >= 7 && strings.EqualFold(q.Text[:7], "perform")
	_, call := q.Query.(*parser.Call)
	switch q.Kind {
	case parser.PLCallSQL:
		return call && len(q.Text) >= 4 && strings.EqualFold(q.Text[:4], "call")
	case parser.PLPerformSQL:
		_, selectQuery := q.Query.(*parser.Select)
		return selectQuery && perform
	case parser.PLCommandSQL:
		return !call && !perform
	}
	return false
}
