package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestConstraintEnforcementParser(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `IF EXISTS "é"`} {
		q := "ALTER TABLE " + target + ` ALTER CONSTRAINT "f é" NOT ENFORCED, ALTER CONSTRAINT second ENFORCED`
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		s := statements[0].(*AlterTable)
		if len(s.Actions) != 2 || s.IfExists != strings.HasPrefix(target, "IF") || s.Only != strings.HasPrefix(target, "ONLY") {
			t.Fatal(s)
		}
		for i, a := range s.Actions {
			if a.Action != AlterConstraintEnforcement || !a.EnforcementSet || a.NotEnforced != (i == 0) || a.Constraint.Text == "" {
				t.Fatal(a)
			}
			if a.EnforcementPos.Character != utf8.RuneCountInString(q[:a.EnforcementPos.Offset])+1 || !strings.HasPrefix(q[a.Pos.Offset:], "ALTER CONSTRAINT") {
				t.Fatal("lost positioned property", a)
			}
		}
	}
	for _, clause := range []string{"CHECK(a>0)", "FOREIGN KEY(a) REFERENCES p"} {
		for _, properties := range []string{"ENFORCED", "NOT ENFORCED", "NOT ENFORCED NOT ENFORCED", "NOT VALID NOT ENFORCED", "NOT ENFORCED NOT VALID"} {
			s, err := Parse("ALTER TABLE t ADD CONSTRAINT fact " + clause + " " + properties)
			if err != nil {
				t.Fatal(clause, properties, err)
			}
			a := s[0].(*AlterTable).Actions[0]
			negative := strings.Contains(properties, "NOT ENFORCED")
			if a.Check != nil && (a.Check.NotEnforced != negative || a.Check.NotEnforced && !a.Check.NotValid || !a.Check.EnforcementSet) {
				t.Fatal(a.Check)
			}
			if a.ForeignKey != nil && (a.ForeignKey.NotEnforced != negative || a.ForeignKey.NotEnforced && !a.ForeignKey.NotValid || !a.ForeignKey.EnforcementSet) {
				t.Fatal(a.ForeignKey)
			}
		}
	}
	for _, q := range []string{
		"ALTER TABLE t ALTER CONSTRAINT fk ENFORCED NOT ENFORCED",
		"ALTER TABLE t ALTER CONSTRAINT fk NOT VALID",
		"ALTER TABLE t ALTER CONSTRAINT fk ENFORCED garbage",
		"ALTER TABLE t ALTER CONSTRAINT fk ENFORCED; INSERT INTO",
		"ALTER DOMAIN positive ADD CHECK(VALUE>0) NOT ENFORCED",
	} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("accepted invalid complete input", q, statements, err)
		}
	}
	// CREATE DOMAIN retains its attribute for the execution-time domain gate.
	s, err := Parse("CREATE DOMAIN positive AS INT CHECK(VALUE>0) NOT ENFORCED")
	if err != nil {
		t.Fatal(err)
	}
	c := s[0].(*CreateDomain).Constraints[0]
	if !c.EnforcementSet || !c.NotEnforced || c.EnforcementPos.Character <= 0 {
		t.Fatal(c)
	}
}

func TestConstraintEnforcementPropertySyntax(t *testing.T) {
	for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
		for _, suffix := range []string{"", " CHECK(a>0)"} {
			q := `CREATE TABLE "é"(a INT ` + attribute + suffix + ")"
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			column := statements[0].(*CreateTable).Elements[0].Column
			orphan := column.Constraints[0]
			if !orphan.EnforcementSet || orphan.NotEnforced != (attribute == "NOT ENFORCED") || orphan.Check != nil || orphan.ForeignKey != nil || orphan.EnforcementPos.Character != utf8.RuneCountInString(q[:orphan.EnforcementPos.Offset])+1 {
				t.Fatal("lost positioned orphan syntax", column)
			}
			if suffix != "" && (len(column.Constraints) != 2 || column.Constraints[1].Check == nil || column.Constraints[1].Check.NotEnforced) {
				t.Fatal("later primary absorbed orphan", column)
			}
		}
		for _, property := range []string{"NOT DEFERRABLE", "INITIALLY IMMEDIATE", "NOT DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE", "INITIALLY DEFERRED", "DEFERRABLE INITIALLY DEFERRED"} {
			for _, properties := range []string{attribute + " " + property, property + " " + attribute} {
				for _, q := range []string{
					"CREATE TABLE t(a INT REFERENCES p " + properties + ")",
					"CREATE TABLE t(a INT,FOREIGN KEY(a) REFERENCES p " + properties + ")",
					"ALTER TABLE t ADD FOREIGN KEY(a) REFERENCES p " + properties,
				} {
					if statements, err := Parse(q); err != nil || len(statements) != 1 {
						t.Fatal(q, statements, err)
					}
				}
			}
		}
	}
	for _, q := range []string{
		"CREATE TABLE t(a INT REFERENCES p ENFORCED INITIALLY \"immediate\")",
	} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("expanded quoted-property boundary", q, statements, err)
		}
	}
}

func TestConstraintEnforcementUsingIndexParser(t *testing.T) {
	for _, kind := range []string{"PRIMARY KEY", "UNIQUE"} {
		for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
			for _, name := range []string{"", `CONSTRAINT "clé é" `} {
				q := "ALTER TABLE t ADD " + name + kind + ` USING INDEX "index é" ` + attribute
				statements, err := Parse(q)
				want := sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000",
					Message:  kind + " constraints cannot be marked " + attribute,
					Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, attribute)]) + 1),
				}
				var diagnostic *sqlstate.Error
				if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
					t.Fatalf("%s: statements=%#v diagnostic=%#v error=%v want=%#v", q, statements, diagnostic, err, want)
				}
			}
		}
	}
}
