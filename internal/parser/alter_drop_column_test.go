package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterDropColumnSyntax(t *testing.T) {
	for _, q := range []string{`ALTER TABLE t DROP b`, `ALTER TABLE t DROP COLUMN b RESTRICT`, `ALTER TABLE t* DROP COLUMN IF EXISTS b CASCADE`, `ALTER TABLE ONLY t DROP b`, `ALTER TABLE ONLY(t) DROP COLUMN b`, `ALTER TABLE "S"."T" DROP COLUMN "COLUMN"`, `ALTER TABLE t DROP "column"`, `ALTER TABLE t DROP COLUMN "column"`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s, ok := statements[0].(*AlterTable)
		if !ok || s.Pos.Character != 1 || len(s.Actions) != 1 || s.Actions[0].Column.Pos.Character == 0 {
			t.Fatal(q, s)
		}
		switch q {
		case `ALTER TABLE "S"."T" DROP COLUMN "COLUMN"`:
			if s.Actions[0].Column.Text != "COLUMN" {
				t.Fatal("quoted keyword target changed", s.Actions[0].Column)
			}
		case `ALTER TABLE t DROP "column"`, `ALTER TABLE t DROP COLUMN "column"`:
			if s.Actions[0].Column.Text != "column" {
				t.Fatal("quoted keyword target changed", s.Actions[0].Column)
			}
		}
	}
	for _, q := range []string{`ALTER TABLE t DROP COLUMN )`, `ALTER TABLE t DROP b garbage`, `ALTER TABLE ONLY(t DROP b`, `ALTER TABLE t DROP COLUMN b; SELECT * FROM`} {
		if s, err := Parse(q); err == nil || s != nil {
			t.Fatal("accepted incomplete input", q, s, err)
		}
	}
}

func TestAlterDropColumnDefaultMarker(t *testing.T) {
	q := "INSERT INTO z DEFAULT VALUES"
	statements, err := Parse(q)
	var got *sqlstate.Error
	var want sqlstate.Error
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*Insert)
	if !s.DefaultValues || len(s.Rows) != 1 || len(s.Rows[0]) != 0 || len(s.Targets) != 0 {
		t.Fatal(s)
	}
	_, err = Parse("INSERT INTO z VALUES ()")
	want = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "23"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got)
	}
}

func TestAlterDropColumnCapabilityBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, message, position string }{
		{"ALTER TABLE t ADD COLUMN c INT", "ALTER TABLE action is unsupported", "15"},
	} {
		_, err := Parse(tc.q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: tc.position}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
}

func TestAlterDropColumnCharacterPositions(t *testing.T) {
	_, err := Parse("/*é*/ ALTER TABLE t DROP COLUMN )")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "33"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got)
	}
}
