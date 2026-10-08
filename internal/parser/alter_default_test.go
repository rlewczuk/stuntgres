package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestAlterDefaultGrammar(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `"s"."t"`} {
		for _, expression := range []string{"7", "TRUE OR FALSE", "NOT FALSE", "NULL IS NULL", "CASE WHEN TRUE THEN 7 ELSE 9 END", "CAST(7 AS INT)"} {
			statements, err := Parse("ALTER TABLE " + target + " ALTER COLUMN i SET DEFAULT " + expression)
			if err != nil {
				t.Fatal(target, expression, err)
			}
			s, ok := statements[0].(*AlterTableDefault)
			if !ok || s.Action != SetDefault || s.Expression == nil || s.Column.Text != "i" || s.Only != strings.HasPrefix(target, "ONLY") || s.Pos.Character != 1 {
				t.Fatal(s)
			}
		}
		statements, err := Parse("ALTER TABLE " + target + " ALTER i DROP DEFAULT")
		if err != nil {
			t.Fatal(err)
		}
		s := statements[0].(*AlterTableDefault)
		if s.Action != DropDefault || s.Expression != nil {
			t.Fatal(s)
		}
	}
	for _, expr := range []string{"TRUE OR FALSE", "NOT FALSE", "NULL IS NULL"} {
		if _, err := Parse("CREATE TABLE t(b BOOL DEFAULT " + expr + ")"); err == nil {
			t.Fatal("CREATE grammar widened", expr)
		}
		if _, err := Parse("CREATE TABLE t(b BOOL DEFAULT (" + expr + "))"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAlterDefaultBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, token, code, message string }{
		{`ALTER TABLE "ét" ALTER i SET DEFAULT`, "", "42601", "syntax error at end of input"},
		{"ALTER TABLE t ALTER i SET DEFAULT 7, ALTER i DROP DEFAULT", ",", "0A000", "ALTER TABLE action lists are unsupported"},
		{"ALTER TABLE t ALTER i DROP DEFAULT, ALTER i SET DEFAULT 7", ",", "0A000", "ALTER TABLE action lists are unsupported"},
		{"ALTER TABLE t ALTER i SET DEFAULT 7 garbage", "garbage", "42601", `syntax error at or near "garbage"`},
	} {
		position := utf8.RuneCountInString(tc.q) + 1
		if tc.token != "" {
			position = utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1
		}
		statements, err := Parse(tc.q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: strconv.Itoa(position)}
		if statements != nil || !errors.As(err, &got) || *got != want {
			t.Fatal(tc.q, statements, err, got, want)
		}
	}
	for _, q := range []string{"ALTER TABLE IF EXISTS t ALTER i SET DEFAULT 7", "ALTER TABLE t ALTER i SET DEFAULT *", "ALTER TABLE t ALTER i DROP DEFAULT 7"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal(q, statements, err)
		}
	}
}

func TestAlterDefaultInheritanceBoundary(t *testing.T) {
	q := "CREATE TABLE child(i INT) INHERITS(parent)"
	statements, err := Parse(q)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "table inheritance is unsupported", Position: strconv.Itoa(strings.Index(q, "INHERITS") + 1)}
	if statements != nil || !errors.As(err, &got) || *got != want {
		t.Fatal(statements, err, got, want)
	}
}
