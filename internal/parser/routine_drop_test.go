package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRoutineDropParser(t *testing.T) {
	for _, kind := range []string{"FUNCTION", "PROCEDURE", "ROUTINE"} {
		statements, err := Parse("/*é*/ DROP " + kind + " IF EXISTS public.f(IN ignored int, OUT nope absent.t), g CASCADE")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		d, ok := statements[0].(*DropRoutine)
		if !ok || d.Pos.Offset != 7 || !d.IfExists || d.Mode != DropCascade || len(d.Targets) != 2 || !d.Targets[0].SignatureSpecified || d.Targets[1].SignatureSpecified {
			t.Fatalf("drop AST: %#v", d)
		}
		if d.Targets[0].Arguments[0].Mode != "in" || d.Targets[0].Arguments[1].Mode != "out" {
			t.Fatal(d.Targets[0].Arguments)
		}
	}
	statements, err := Parse("DROP FUNCTION f()")
	if err != nil {
		t.Fatal(err)
	}
	if d := statements[0].(*DropRoutine); d.Mode != DropRestrict || !d.Targets[0].SignatureSpecified || len(d.Targets[0].Arguments) != 0 {
		t.Fatal(d)
	}
	for _, q := range []string{"DROP FUNCTION f() trailing", "DROP ROUTINE f(),", "DROP PROCEDURE", "DROP FUNCTION f() CASCADE RESTRICT"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted incomplete DROP", q)
		}
	}
	for _, tc := range []struct {
		source string
		count  int
		code   string
	}{
		{"SELECT missing(1), $1", 0, "42P02"},
		{"SELECT (SELECT $2), missing(1)", 1, "42P02"},
		{"SELECT missing(1), $1", 1, ""},
		{"SELECT '$9', $$ $8 $$ /* $7 */ -- $6\n", 0, ""},
	} {
		_, err := ParseWithOptions(tc.source, Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: tc.count})
		var diagnostic *sqlstate.Error
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc.source, err)
			}
		} else if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc.source, err)
		}
	}
}
