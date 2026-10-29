package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAlterConstraintModesGrammar(t *testing.T) {
	for _, tc := range []struct {
		property                                string
		kind                                    AlterTableActionKind
		inherit, noinherit, timing, enforcement bool
	}{
		{"", AlterConstraint, false, false, false, false},
		{"INHERIT", AlterConstraintInherit, true, false, false, false},
		{"NO INHERIT", AlterConstraintInherit, true, true, false, false},
		{"NO INHERIT NO INHERIT", AlterConstraintInherit, true, true, false, false},
		{"NO INHERIT DEFERRABLE", AlterConstraintInherit, true, true, true, false},
		{"DEFERRABLE NO INHERIT", AlterConstraintTiming, true, true, true, false},
		{"NO INHERIT NOT ENFORCED", AlterConstraintInherit, true, true, false, true},
	} {
		q := `/* é */ ALTER TABLE ONLY "s"."t" ALTER CONSTRAINT "inherit"`
		if tc.property != "" {
			q += " " + tc.property
		}
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s := statements[0].(*AlterTable)
		if !s.Only || len(s.Actions) != 1 {
			t.Fatal(s)
		}
		a := s.Actions[0]
		if a.Action != tc.kind || a.Constraint.Text != "inherit" || a.InheritabilitySet != tc.inherit || a.NoInherit != tc.noinherit || a.DeferrabilitySet != tc.timing || a.EnforcementSet != tc.enforcement {
			t.Fatal(q, a)
		}
		offset := strings.Index(q, `"inherit"`)
		if a.Constraint.Pos.Offset != offset || a.Constraint.Pos.Character != utf8.RuneCountInString(q[:offset])+1 || !strings.HasPrefix(q[a.Pos.Offset:], "ALTER CONSTRAINT") {
			t.Fatal("lost positioned names", q, a)
		}
	}
	for _, tail := range []string{"INHERIT DEFERRABLE", "INHERIT NO INHERIT", `"inherit"`, `NO "inherit"`, "NO INHERIT garbage", "DEFERRABLE NOT DEFERRABLE", "NOT DEFERRABLE INITIALLY DEFERRED"} {
		if statements, err := Parse("ALTER TABLE t ALTER CONSTRAINT nn " + tail); err == nil || statements != nil {
			t.Fatal("accepted malformed property suffix", tail, statements, err)
		}
	}
}

func TestRestoreNotNullModeParserIsolation(t *testing.T) {
	const q = "ALTER TABLE public.t ADD CONSTRAINT nn NOT NULL a NOT VALID NO INHERIT"
	for _, enabled := range []bool{false, true, false} {
		s, err := ParseWithOptions(q, Options{StandardConformingStrings: true, NotNullInheritReplay: enabled})
		if !enabled {
			if err == nil || s != nil {
				t.Fatal("private constructor escaped into ordinary parse", s, err)
			}
			continue
		}
		if err != nil || len(s) != 1 {
			t.Fatal(s, err)
		}
		a := s[0].(*AlterTable).Actions[0]
		if a.Action != AddNotNull || a.Constraint.Text != "nn" || a.Column.Text != "a" || !a.NotValid || !a.NoInherit {
			t.Fatal(a)
		}
	}
}
