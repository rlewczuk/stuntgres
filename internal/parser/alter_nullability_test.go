package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterNullabilitySyntax(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `"s"."t"`} {
		ast, err := Parse("ALTER TABLE " + target + ` ALTER COLUMN "a" SET NOT NULL, DROP CONSTRAINT IF EXISTS "n" CASCADE, ADD CONSTRAINT nn NOT NULL b, DROP COLUMN IF EXISTS c RESTRICT, ALTER b DROP NOT NULL`)
		if err != nil {
			t.Fatal(target, err)
		}
		s, ok := ast[0].(*AlterTable)
		if !ok || len(s.Actions) != 5 || s.Pos.Character != 1 {
			t.Fatal(s)
		}
		var kinds []AlterTableActionKind
		for _, a := range s.Actions {
			if a.Pos.Character == 0 {
				t.Fatal(a)
			}
			kinds = append(kinds, a.Action)
		}
		if !reflect.DeepEqual(kinds, []AlterTableActionKind{SetNotNull, DropConstraint, AddNotNull, DropColumn, DropNotNull}) {
			t.Fatal(kinds)
		}
		if s.Actions[0].Column.Text != "a" || s.Actions[1].Constraint.Text != "n" || !s.Actions[1].IfExists || s.Actions[1].Mode != DropCascade || s.Actions[2].Constraint.Text != "nn" || s.Actions[3].Mode != DropRestrict {
			t.Fatal(s.Actions)
		}
	}
	_, err := Parse("ALTER TABLE t ALTER a SET NOT NULL,")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at end of input", Position: "36"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err, want)
	}
	for _, q := range []string{"CREATE TABLE prefix(a INT); ALTER TABLE t ALTER a SET NOT NULL,", "ALTER TABLE t ALTER a SET NOT NULL, RENAME TO other", "ALTER TABLE t DROP b, DROP", "ALTER TABLE t ALTER a SET NOT NULL, ALTER a SET DEFAULT 7"} {
		if ast, err := Parse(q); err == nil || ast != nil {
			t.Fatal("accepted a valid prefix", q, ast, err)
		}
	}
}
