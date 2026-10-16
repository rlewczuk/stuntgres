package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDomainAlterConstraintGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql                          string
		action                       DomainConstraintAction
		name                         string
		invalid, noInherit, ifExists bool
		mode                         DropMode
	}{
		{`ALTER DOMAIN /* é */ domain_s."Δ Domain" ADD CONSTRAINT "é positive" CHECK(VALUE>0) NO INHERIT NOT VALID`, DomainAddCheck, "é positive", true, true, false, DropRestrict},
		{`ALTER DOMAIN domain_s.d ADD CHECK(VALUE<10)`, DomainAddCheck, "", false, false, false, DropRestrict},
		{`ALTER DOMAIN domain_s.d DROP CONSTRAINT IF EXISTS positive CASCADE`, DomainDropCheck, "positive", false, false, true, DropCascade},
		{`ALTER DOMAIN domain_s.d DROP CONSTRAINT positive RESTRICT`, DomainDropCheck, "positive", false, false, false, DropRestrict},
		{`ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive`, DomainValidateCheck, "positive", false, false, false, DropRestrict},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		alter, ok := statements[0].(*AlterDomain)
		if !ok || alter.Action != tc.action || alter.Constraint.Text != tc.name || alter.IfExists != tc.ifExists || alter.Mode != tc.mode || len(alter.Name.Parts) != 2 || alter.Name.Parts[0].Text != "domain_s" || alter.Pos.Character != 1 {
			t.Fatal("lost narrow utility facts", tc, statements[0])
		}
		if tc.action == DomainAddCheck {
			if alter.Check == nil || alter.Check.NotValid != tc.invalid || alter.Check.NoInherit != tc.noInherit || alter.Check.Name != alter.Constraint {
				t.Fatal("lost CHECK facts", tc, alter.Check)
			}
			offset := strings.Index(tc.sql, "CHECK")
			if alter.Check.Pos.Offset != offset || alter.Check.Pos.Character != utf8.RuneCountInString(tc.sql[:offset])+1 {
				t.Fatal("CHECK byte/character position", alter.Check.Pos)
			}
		} else if alter.Check != nil {
			t.Fatal("non-ADD acquired a CHECK")
		}
	}
	for _, sql := range []string{
		"ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>)",
		"ALTER DOMAIN domain_s.d ADD CHECK(VALUE>0), DROP CONSTRAINT positive",
		"ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive nonsense",
		"ALTER DOMAIN domain_s.d SET DEFAULT 7",
		"ALTER DOMAIN domain_s.d SET NOT NULL",
		"CREATE DOMAIN domain_s.d AS integer CHECK(VALUE>0) NOT VALID",
		"ALTER DOMAIN domain_s.d ADD CHECK(VALUE>0); ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT",
	} {
		if statements, err := Parse(sql); err == nil || statements != nil {
			t.Fatal("incomplete/adjacent syntax admitted", sql, statements, err)
		}
	}
}
