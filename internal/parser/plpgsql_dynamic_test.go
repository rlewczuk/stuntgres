package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLDynamicSQLParser(t *testing.T) {
	for _, statement := range []string{
		"EXECUTE ('SELECT ''INTO USING''') INTO STRICT x USING coalesce(1,2),(SELECT 3)",
		"EXECUTE /* é中 */ command USING coalesce(1,2),3 INTO x",
		"EXECUTE command INTO r.a,r.b USING 1,2",
		"RETURN QUERY EXECUTE command USING 1,2",
	} {
		source := "DECLARE x integer; r record; BEGIN\n " + statement + "; END"
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(statement, err)
		}
		raw := block.Statements[0]
		q := raw.Dynamic
		if q == nil || !ValidPLDynamicStatement(q) || q.ReturnQuery != strings.HasPrefix(statement, "RETURN QUERY") || len(q.Using) != 2 || q.Command.Expression == nil || q.End.Offset != strings.LastIndex(source, ";") {
			t.Fatal("lost dynamic variant or boundaries", raw)
		}
		for _, operand := range append([]PLExpression{q.Command}, q.Using...) {
			if operand.Start.Character != utf8.RuneCountInString(source[:operand.Start.Offset])+1 || operand.End.Offset > operand.Delimiter.Offset || operand.Start.Offset >= operand.End.Offset {
				t.Fatal("invalid operand span", operand)
			}
		}
		q.ReturnQuery = !q.ReturnQuery
		if ValidPLDynamicStatement(q) {
			t.Fatal("accepted coordinated mode forgery")
		}
	}
	for _, statement := range []string{"EXECUTE", "EXECUTE 'SELECT 1' INTO", "EXECUTE 'SELECT 1' USING", "EXECUTE 'SELECT 1' INTO x INTO x", "EXECUTE 'SELECT 1' USING 1,", "EXECUTE 'SELECT 1' INTO x[1]", "RETURN QUERY SELECT 1"} {
		if _, err := ParsePLpgSQL("DECLARE x integer; BEGIN "+statement+"; END", PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed dynamic statement", statement)
		}
	}
	for _, prefix := range []string{"", "/* é中 */ "} {
		source := prefix + "BEGIN EXECUTE 'SELECT 1' USING 1 USING 2; END"
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(utf8.RuneCountInString(source[:strings.Index(source, "2;")]) + 1)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at or near \"2\"" || diagnostic.Position != position {
			t.Fatal("repeated USING operand lost to delimiter remapping", diagnostic, err)
		}
	}
	block, err := ParsePLpgSQL("DECLARE x integer; c refcursor; BEGIN SELECT 1 INTO x; OPEN c FOR EXECUTE 'SELECT 1' USING 2; CLOSE c; END", PLReturnPolicy{})
	if err != nil || block.Statements[0].Into == nil || block.Statements[1].Cursor == nil {
		t.Fatal("changed static/cursor syntax", err)
	}
}
