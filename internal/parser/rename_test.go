package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterRenameSyntax(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO b", "ALTER TABLE ONLY t RENAME COLUMN a TO b", "ALTER TABLE ONLY(t) RENAME a TO b", "ALTER TABLE t* RENAME a TO b", "ALTER TABLE IF EXISTS public.t RENAME COLUMN a TO b", `ALTER TABLE t RENAME "constraint" TO "select"`, "ALTER TABLE if RENAME a TO b", "ALTER TABLE if.t RENAME a TO b", "ALTER TABLE IF EXISTS if RENAME a TO b", `ALTER TABLE "if" RENAME a TO b`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		s, ok := statements[0].(*AlterTableRenameColumn)
		if !ok || s.Column.Text == "" || s.NewName.Text == "" || s.Pos.Character != 1 {
			t.Fatal(q, s)
		}
	}
	for _, q := range []string{"ALTER TABLE t RENAME TO u", "ALTER TABLE IF EXISTS public.t RENAME TO u", "ALTER TABLE t* RENAME TO u", "ALTER TABLE ONLY t RENAME TO u", "ALTER TABLE ONLY(t) RENAME TO u"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if _, ok := statements[0].(*AlterTableRenameTable); !ok {
			t.Fatal(q, statements)
		}
	}
	for _, tc := range []struct{ q, code string }{
		{"ALTER TABLE t RENAME a TO s.b", "42601"}, {"ALTER TABLE t RENAME TO u junk", "42601"}, {"ALTER TABLE IF EXISTS missing RENAME TO )", "42601"},
		{"ALTER TABLE t RENAME a TO b, DROP b", "0A000"},
		{`ALTER TABLE "if" EXISTS t RENAME a TO b`, "42601"},
	} {
		statements, err := Parse("CREATE TABLE prefix(a INT);" + tc.q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || len(statements) != 0 {
			t.Fatal(tc, statements, err)
		}
	}
}
