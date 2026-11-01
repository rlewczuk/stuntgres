package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestParseTriggerTransitionDeclarations(t *testing.T) {
	for _, tc := range []struct {
		clause        string
		newFirst, row bool
	}{
		{`OLD TABLE AS "古" NEW TABLE AS "新"`, false, false},
		{`NEW TABLE "新" OLD TABLE "古"`, true, false},
		{`OLD ROW "古" NEW ROW AS "新"`, false, true},
	} {
		q := `CREATE OR REPLACE TRIGGER "名" AFTER UPDATE ON public.t REFERENCING ` + tc.clause + ` FOR EACH STATEMENT EXECUTE FUNCTION f()`
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s := statements[0].(*CreateTrigger)
		if !s.Replace || s.Row || len(s.Transitions) != 2 {
			t.Fatal("lost declaration options", s)
		}
		for i, declaration := range s.Transitions {
			wantNew := tc.newFirst == (i == 0)
			word, alias := "OLD", "古"
			if wantNew {
				word, alias = "NEW", "新"
			}
			offset := strings.Index(q, word+" ")
			aliasOffset := strings.Index(q, `"`+alias+`"`)
			if declaration.New != wantNew || declaration.Row != tc.row || declaration.Alias.Text != alias || declaration.Pos.Offset != offset || declaration.Pos.Character != utf8.RuneCountInString(q[:offset])+1 || declaration.Alias.Pos.Offset != aliasOffset || declaration.Alias.Pos.Character != utf8.RuneCountInString(q[:aliasOffset])+1 {
				t.Fatalf("lost ordered positioned declaration: %+v", declaration)
			}
		}
	}
	// Duplicates and invalid event mappings are semantic, not parser exclusions.
	for _, clause := range []string{"OLD TABLE a OLD TABLE b", "NEW TABLE a NEW TABLE b", "OLD TABLE same NEW TABLE same", "OLD ROW a"} {
		q := "CREATE TRIGGER x AFTER INSERT ON t REFERENCING " + clause + " EXECUTE FUNCTION f()"
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{
		"CREATE TRIGGER x AFTER INSERT ON t REFERENCING EXECUTE FUNCTION f()",
		"CREATE TRIGGER x AFTER INSERT ON t REFERENCING NEW TABLE AS EXECUTE FUNCTION f()",
		"CREATE TRIGGER x AFTER INSERT ON t REFERENCING NEW TABLE s.x EXECUTE FUNCTION f()",
		"CREATE TRIGGER x AFTER INSERT ON t REFERENCING OTHER TABLE x EXECUTE FUNCTION f()",
		"CREATE TRIGGER x AFTER INSERT ON t REFERENCING NEW TABLE x EXECUTE FUNCTION f() garbage",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("accepted malformed suffix", q, statements, err)
		}
	}
	for _, q := range []string{"CREATE TABLE referencing(referencing int)", "SELECT referencing AS referencing FROM referencing", "CREATE TRIGGER referencing AFTER INSERT ON referencing REFERENCING NEW TABLE referencing EXECUTE FUNCTION referencing()"} {
		if _, err := Parse(q); err != nil {
			t.Fatal("unreserved keyword changed identifier capability", q, err)
		}
	}
}

func TestParseTriggerTransitionConstraintDiagnostic(t *testing.T) {
	for _, create := range []string{"CREATE", "CREATE OR REPLACE"} {
		for _, options := range []string{"", " FROM public.other", " DEFERRABLE INITIALLY DEFERRED", " NOT DEFERRABLE INITIALLY IMMEDIATE"} {
			q := create + ` CONSTRAINT TRIGGER "名" AFTER UPDATE ON public.t` + options + ` REFERENCING OLD TABLE o FOR EACH ROW EXECUTE FUNCTION f()`
			statements, err := Parse(q)
			var got *sqlstate.Error
			offset := strings.Index(q, "REFERENCING")
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "REFERENCING"`, Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
			if len(statements) != 0 || !errors.As(err, &got) || *got != want {
				t.Fatalf("%s: %#v %v want %#v", q, got, err, want)
			}
		}
	}
	q := "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON t FOR EACH ROW EXECUTE FUNCTION f()"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal("constraint declaration rejected without REFERENCING", statements, err)
	}
	s := statements[0].(*CreateTrigger)
	if !s.Constraint || !s.Row || len(s.Transitions) != 0 {
		t.Fatal("constraint declaration lost metadata", s)
	}
}
