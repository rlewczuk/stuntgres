package parser

import (
	"fmt"
	"strconv"
	"strings"
)

// PLDiagnosticItem identifies only released GET items, not server error fields.
type PLDiagnosticItem uint8

const (
	PLRowCount PLDiagnosticItem = iota + 1
	PLRoutineOID
	PLContext
	PLReturnedSQLState
	PLMessageText
	PLExceptionDetail
	PLExceptionHint
	PLExceptionContext
	PLSchemaName
	PLTableName
	PLColumnName
	PLDatatypeName
	PLConstraintName
)

var plDiagnosticNames = [...]string{"", "ROW_COUNT", "PG_ROUTINE_OID", "PG_CONTEXT", "RETURNED_SQLSTATE", "MESSAGE_TEXT", "PG_EXCEPTION_DETAIL", "PG_EXCEPTION_HINT", "PG_EXCEPTION_CONTEXT", "SCHEMA_NAME", "TABLE_NAME", "COLUMN_NAME", "PG_DATATYPE_NAME", "CONSTRAINT_NAME"}

func (i PLDiagnosticItem) String() string {
	if i < PLRowCount || int(i) >= len(plDiagnosticNames) {
		return ""
	}
	return plDiagnosticNames[i]
}
func (i PLDiagnosticItem) Allowed(stacked bool) bool {
	return i.String() != "" && (i == PLContext || stacked && i >= PLReturnedSQLState || !stacked && i <= PLRoutineOID)
}

type PLDiagnosticAssignment struct {
	Target  Expr
	Item    PLDiagnosticItem
	ItemPos Position
}
type PLDiagnostics struct {
	Stacked         bool
	Pos             Position
	Assignments     []PLDiagnosticAssignment
	End, Delimiter  Position
	Text            string
	admittedStacked bool
	admittedPos     Position
	admitted        []plDiagnosticAdmission
	admittedEnd     Position
	admittedText    string
}
type plDiagnosticAdmission struct {
	target  string
	at      Position
	item    PLDiagnosticItem
	itemPos Position
}

// A detached target fingerprint prevents coordinated raw/checked mutations from
// replacing parser admission. It is computed only during admission/validation.
func plDiagnosticTarget(e Expr) string {
	switch e := e.(type) {
	case ColumnExpr:
		var b strings.Builder
		for _, p := range e.Name.Parts {
			b.WriteString(strconv.Quote(p.Text))
			fmt.Fprint(&b, p.Pos)
		}
		return b.String()
	case ParameterExpr:
		return fmt.Sprintf("$%d:%v", e.Number, e.Pos)
	case FieldExpr:
		return plDiagnosticTarget(e.Base) + "." + strconv.Quote(e.Field.Text) + fmt.Sprint(e.Field.Pos)
	case SubscriptExpr:
		return plDiagnosticTarget(e.Base) + "[]"
	}
	return ""
}
func ValidPLDiagnostics(d *PLDiagnostics) bool {
	if d == nil || d.Stacked != d.admittedStacked || d.Pos != d.admittedPos || d.End != d.admittedEnd || d.Delimiter != d.admittedEnd || d.Text != d.admittedText || len(d.Assignments) == 0 || len(d.Assignments) != len(d.admitted) {
		return false
	}
	for n, a := range d.Assignments {
		old := d.admitted[n]
		if a.Target == nil || !a.Item.Allowed(d.Stacked) || old.item != a.Item || old.itemPos != a.ItemPos || old.at != a.Target.SourcePosition() || old.target != plDiagnosticTarget(a.Target) {
			return false
		}
	}
	return true
}
func (l *lexer) plDiagnosticItem(t lexeme) (PLDiagnosticItem, bool) {
	if !quotedIdentifierLexeme(t) {
		name := strings.ToUpper(t.text)
		for i := PLRowCount; int(i) < len(plDiagnosticNames); i++ {
			if name == i.String() {
				return i, true
			}
		}
	}
	raw := t.raw
	if raw == "" {
		raw = t.text
	}
	l.failAt("42601", `unrecognized GET DIAGNOSTICS item at or near "`+raw+`"`, t.pos.Offset)
	return 0, false
}
func (l *lexer) finishPLDiagnostics(d *PLDiagnostics) bool {
	d.admittedStacked, d.admittedPos = d.Stacked, d.Pos
	for _, a := range d.Assignments {
		if !a.Item.Allowed(d.Stacked) {
			area := "CURRENT"
			if d.Stacked {
				area = "STACKED"
			}
			l.failAt("42601", "diagnostics item "+a.Item.String()+" is not allowed in GET "+area+" DIAGNOSTICS", d.Pos.Offset)
			return false
		}
		d.admitted = append(d.admitted, plDiagnosticAdmission{target: plDiagnosticTarget(a.Target), at: a.Target.SourcePosition(), item: a.Item, itemPos: a.ItemPos})
	}
	return true
}

// finishSource records the exact occurrence at the statement-list delimiter,
// just like finishSQL. The text borrows immutable input, not lexer state.
func (d *PLDiagnostics) finishSource(source string, delimiter Position) {
	d.End, d.Delimiter = delimiter, delimiter
	d.Text = source[d.Pos.Offset:delimiter.Offset]
	d.admittedEnd, d.admittedText = delimiter, d.Text
}
