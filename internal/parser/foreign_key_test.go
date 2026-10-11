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
}

func TestForeignKeyGrammarBoundaries(t *testing.T) {
	for _, tc := range []struct{ suffix, token, message string }{
		{"MATCH PARTIAL", "MATCH", "MATCH PARTIAL not yet implemented"},
		{"ON DELETE CASCADE", "CASCADE", "foreign key referential actions are unsupported"},
		{"ON UPDATE RESTRICT", "RESTRICT", "foreign key referential actions are unsupported"},
		{"ON DELETE SET NULL", "SET", "foreign key referential actions are unsupported"},
		{"ON UPDATE SET DEFAULT", "SET", "foreign key referential actions are unsupported"},
		{"DEFERRABLE", "DEFERRABLE", "foreign key deferrability is unsupported"},
		{"INITIALLY DEFERRED", "DEFERRED", "foreign key deferrability is unsupported"},
	} {
		q := `CREATE TABLE "é"(pid INT REFERENCES p ` + tc.suffix + ")"
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message || diagnostic.Position != position {
			t.Fatal(q, statements, diagnostic, err)
		}
	}
	for _, q := range []string{`ALTER TABLE c ADD FOREIGN KEY(a) REFERENCES p`, `ALTER TABLE c ADD a INT REFERENCES p`} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "0A000" || d.Message != "ALTER TABLE action is unsupported" {
			t.Fatal(q, statements, err)
		}
	}
}

func TestForeignKeyGrammarCompleteInput(t *testing.T) {
	for _, q := range []string{
		`CREATE TABLE prefix(a INT);CREATE TABLE c(a INT REFERENCES p MATCH PARTIAL) trailing`,
		`CREATE TABLE c(a INT,FOREIGN KEY(a) CONSTRAINT fk REFERENCES p)`,
		`CREATE TABLE c(a INT REFERENCES p ON DELETE NO nonsense)`,
		`CREATE TABLE c(a INT REFERENCES p MATCH "full")`,
		`CREATE TABLE c(a INT REFERENCES p(id,))`,
	} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}
