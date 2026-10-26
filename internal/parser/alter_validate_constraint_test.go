package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAlterValidateConstraintParser(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `IF EXISTS "é"`} {
		q := `ALTER TABLE ` + target + ` VALIDATE CONSTRAINT "N é", VALIDATE CONSTRAINT second`
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		s := statements[0].(*AlterTable)
		if len(s.Actions) != 2 || s.IfExists != strings.HasPrefix(target, "IF") || s.Only != strings.HasPrefix(target, "ONLY") {
			t.Fatal(s)
		}
		for i, name := range []string{"N é", "second"} {
			a := s.Actions[i]
			if a.Action != ValidateConstraint || a.Constraint.Text != name || a.Column.Text != "" {
				t.Fatal(a)
			}
			if a.Pos.Character != utf8.RuneCountInString(q[:a.Pos.Offset])+1 || a.Constraint.Pos.Character != utf8.RuneCountInString(q[:a.Constraint.Pos.Offset])+1 {
				t.Fatal("lost positioned action", a)
			}
			if !strings.HasPrefix(q[a.Pos.Offset:], "VALIDATE") {
				t.Fatal(a.Pos)
			}
		}
	}
	for _, q := range []string{
		`ALTER TABLE t "validate" CONSTRAINT ck`,
		`ALTER TABLE t arbitrary CONSTRAINT ck`,
		`ALTER TABLE t VALIDATE CONSTRAINT`,
		`ALTER TABLE t VALIDATE CONSTRAINT IF EXISTS ck`,
		`ALTER TABLE t VALIDATE CONSTRAINT ck NOT VALID`,
		`ALTER TABLE t VALIDATE CONSTRAINT ck garbage`,
		`ALTER TABLE t VALIDATE CONSTRAINT ck; INSERT INTO`,
		`ALTER DOMAIN d "validate" CONSTRAINT ck`,
	} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("accepted malformed complete input", q, statements, err)
		}
	}
	if _, err := Parse(`ALTER DOMAIN d VALIDATE CONSTRAINT ck`); err != nil {
		t.Fatal(err)
	}
}
