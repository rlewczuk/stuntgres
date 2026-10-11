package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestForeignKeyGrammar(t *testing.T) {
	for _, q := range []string{
		`CREATE TABLE c(a INT CONSTRAINT fk REFERENCES public.p(id) MATCH FULL ON UPDATE NO ACTION ON DELETE NO ACTION NOT DEFERRABLE INITIALLY IMMEDIATE)`,
		`CREATE TABLE c(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES public.p(id) MATCH FULL INITIALLY IMMEDIATE NOT DEFERRABLE)`,
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		raw := statements[0].(*CreateTable)
		var f *ForeignKeyConstraint
		for _, element := range raw.Elements {
			if element.ForeignKey != nil {
				f = element.ForeignKey
			}
			if element.Column != nil {
				for _, constraint := range element.Column.Constraints {
					if constraint.ForeignKey != nil {
						f = constraint.ForeignKey
					}
				}
			}
		}
		if f == nil || f.Name.Text != "fk" || !f.Full || len(f.Target.Parts) != 2 || f.Target.Parts[0].Text != "public" || f.Target.Parts[1].Text != "p" || len(f.Referenced) != 1 || f.Referenced[0].Text != "id" || f.Pos.Character <= 0 {
			t.Fatal(f)
		}
	}
	for _, q := range []string{`CREATE TABLE c(a INT REFERENCES p)`, `CREATE TABLE c(a INT,b INT,FOREIGN KEY(a,b) REFERENCES p(b,a) MATCH SIMPLE)`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	for _, tc := range []struct {
		action string
		value  ForeignKeyAction
	}{
		{"NO ACTION", ForeignKeyNoAction}, {"RESTRICT", ForeignKeyRestrict}, {"CASCADE", ForeignKeyCascade}, {"SET NULL", ForeignKeySetNull}, {"SET DEFAULT", ForeignKeySetDefault},
	} {
		q := "CREATE TABLE c(a INT,b INT,FOREIGN KEY(a,b) REFERENCES p ON UPDATE " + tc.action + " ON DELETE SET NULL(b,b))"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		f := statements[0].(*CreateTable).Elements[2].ForeignKey
		if f.UpdateAction != tc.value || f.DeleteAction != ForeignKeySetNull || len(f.DeleteColumns) != 2 || f.DeleteColumns[0].Text != "b" || f.DeleteColumns[0].Pos.Character <= 0 || f.DeleteColumns[1].Pos.Character <= f.DeleteColumns[0].Pos.Character {
			t.Fatal(f)
		}
	}
}

func TestForeignKeyGrammarBoundaries(t *testing.T) {
	for _, tc := range []struct{ suffix, token, message string }{
		{"MATCH PARTIAL", "MATCH", "MATCH PARTIAL not yet implemented"},
		{"DEFERRABLE", "DEFERRABLE", "foreign key deferrability is unsupported"},
		{"INITIALLY DEFERRED", "DEFERRED", "foreign key deferrability is unsupported"},
		{"ON UPDATE SET NULL(a)", "ON UPDATE", "a column list with SET NULL is only supported for ON DELETE actions"},
		{"ON UPDATE SET DEFAULT(a)", "ON UPDATE", "a column list with SET DEFAULT is only supported for ON DELETE actions"},
	} {
		q := `CREATE TABLE "é"(pid INT REFERENCES p ` + tc.suffix + ")"
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message || diagnostic.Position != position {
			t.Fatal(q, statements, diagnostic, err)
		}
	}
	for _, q := range []string{`ALTER TABLE c ADD a INT REFERENCES p`} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "0A000" || d.Message != "ALTER TABLE action is unsupported" {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse(`ALTER TABLE c ADD FOREIGN KEY(a) REFERENCES p`)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	action := statements[0].(*AlterTable).Actions[0]
	if action.Action != AddForeignKey || action.ForeignKey == nil || len(action.ForeignKey.Columns) != 1 || action.ForeignKey.Columns[0].Text != "a" || action.ForeignKey.Target.Parts[0].Text != "p" || action.ForeignKey.NotValid {
		t.Fatal(action)
	}
}

func TestForeignKeyGrammarCompleteInput(t *testing.T) {
	for _, q := range []string{
		`CREATE TABLE prefix(a INT);CREATE TABLE c(a INT REFERENCES p MATCH PARTIAL) trailing`,
		`CREATE TABLE c(a INT,FOREIGN KEY(a) CONSTRAINT fk REFERENCES p)`,
		`CREATE TABLE c(a INT REFERENCES p ON DELETE NO nonsense)`,
		`CREATE TABLE c(a INT REFERENCES p MATCH "full")`,
		`CREATE TABLE c(a INT REFERENCES p(id,))`,
		`CREATE TABLE c(a INT REFERENCES p ON DELETE SET NULL())`,
		`CREATE TABLE c(a INT REFERENCES p ON DELETE CASCADE(a))`,
		`CREATE TABLE c(a INT REFERENCES p ON DELETE CASCADE ON DELETE SET NULL)`,
		`CREATE TABLE c(a INT REFERENCES p ON UPDATE CASCADE ON UPDATE RESTRICT)`,
	} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}
