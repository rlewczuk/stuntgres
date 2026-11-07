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
		{"ALTER TABLE t ALTER i SET DEFAULT 7 garbage", "garbage", "42601", `syntax error at or near "garbage"`},
		{"ALTER TABLE t ALTER i SET DEFAULT 1, ALTER i DROP DEFAULT 7", "7", "42601", `syntax error at or near "7"`},
		{"ALTER TABLE t ALTER i SET DEFAULT 1, ALTER i SET DEFAULT *", "*", "42601", `syntax error at or near "*"`},
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
	// A lone default remains the standalone AST, which has no IF EXISTS form.
	{
		statements, err := Parse("ALTER TABLE IF EXISTS t ALTER i SET DEFAULT 7")
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "ALTER"`, Position: "25"}
		if statements != nil || !errors.As(err, &got) || *got != want {
			t.Fatal(statements, err, got, want)
		}
	}
	for _, q := range []string{"ALTER TABLE t ALTER i SET DEFAULT *", "ALTER TABLE t ALTER i DROP DEFAULT 7"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal(q, statements, err)
		}
	}
}

func TestAlterDefaultActionListGrammar(t *testing.T) {
	for _, tc := range []struct {
		q              string
		kinds          []AlterTableActionKind
		columns        []string
		expressions    []bool
		only, star, ie bool
	}{
		{"ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b DROP DEFAULT", []AlterTableActionKind{SetColumnDefault, DropColumnDefault}, []string{"a", "b"}, []bool{true, false}, false, false, false},
		{"ALTER TABLE ONLY(t) ALTER a SET DEFAULT CAST(7 AS INT), ALTER b DROP DEFAULT", []AlterTableActionKind{SetColumnDefault, DropColumnDefault}, []string{"a", "b"}, []bool{true, false}, true, false, false},
		{"ALTER TABLE t* ALTER a SET DEFAULT CASE WHEN TRUE THEN 7 ELSE 9 END, ALTER b DROP DEFAULT", []AlterTableActionKind{SetColumnDefault, DropColumnDefault}, []string{"a", "b"}, []bool{true, false}, false, true, false},
		{"ALTER TABLE IF EXISTS t ALTER a SET DEFAULT 1, ALTER b DROP DEFAULT", []AlterTableActionKind{SetColumnDefault, DropColumnDefault}, []string{"a", "b"}, []bool{true, false}, false, false, true},
		{"ALTER TABLE t ALTER a DROP DEFAULT, ADD COLUMN c INT, ALTER a SET DEFAULT 2", []AlterTableActionKind{DropColumnDefault, AddColumn, SetColumnDefault}, []string{"a", "c", "a"}, []bool{false, false, true}, false, false, false},
	} {
		statements, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		table, ok := statements[0].(*AlterTable)
		if !ok || len(table.Actions) != len(tc.kinds) || table.Only != tc.only || table.Star != tc.star || table.IfExists != tc.ie || table.Pos.Character != 1 {
			t.Fatal(tc.q, statements)
		}
		for i, action := range table.Actions {
			if action.Action != tc.kinds[i] || action.Column.Text != tc.columns[i] || (action.Expression != nil) != tc.expressions[i] {
				t.Fatal(tc.q, i, action)
			}
			if i != 1 || tc.kinds[i] != AddColumn {
				if action.Pos.Character == 0 {
					t.Fatal(tc.q, i, action.Pos)
				}
			}
		}
	}
	for _, q := range []string{"ALTER TABLE t ALTER i SET DEFAULT 7", "ALTER TABLE ONLY t ALTER i DROP DEFAULT"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if _, ok := statements[0].(*AlterTableDefault); !ok {
			t.Fatal(q, statements)
		}
	}
	statements, err := Parse("ALTER MATERIALIZED VIEW mv ALTER COLUMN a SET DEFAULT 1")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "ALTER MATERIALIZED VIEW action is unsupported", Position: "28"}
	if statements != nil || !errors.As(err, &got) || *got != want {
		t.Fatal(statements, err, got, want)
	}
	if statements, err = Parse("ALTER VIEW v ALTER COLUMN a SET DEFAULT 1"); err == nil || statements != nil {
		t.Fatal(statements, err)
	}
}

func TestAlterDefaultInheritanceAST(t *testing.T) {
	statements, err := Parse("CREATE TABLE child(i INT DEFAULT 7) INHERITS(parent)")
	if err != nil {
		t.Fatal(err)
	}
	table := statements[0].(*CreateTable)
	if len(table.Parents) != 1 || table.Parents[0].Parts[0].Text != "parent" || len(table.Elements) != 1 || table.Elements[0].Column.Constraints[0].Default == nil {
		t.Fatal(table)
	}
}
