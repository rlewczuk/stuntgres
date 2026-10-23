package parser

import (
	"strings"
	"testing"
)

func TestRoutineAlterParser(t *testing.T) {
	for _, kind := range []string{"FUNCTION", "PROCEDURE", "ROUTINE"} {
		for _, action := range []struct{ sql, kind, target string }{
			{`RENAME TO "New Name"`, "rename", "New Name"},
			{`SET SCHEMA "New Schema"`, "schema", "New Schema"},
			{"OWNER TO CURRENT_ROLE", "owner", "current_role"},
		} {
			source := `/*é*/ ALTER ` + kind + ` "Quoted Schema"."Old Name"(IN ignored int, OUT noise absent.t) ` + action.sql
			statements, err := Parse(source)
			if err != nil || len(statements) != 1 {
				t.Fatal(source, statements, err)
			}
			d, ok := statements[0].(*AlterRoutine)
			if !ok || d.Pos.Offset != 7 || d.Kind != strings.ToLower(kind) || d.Action != action.kind || d.Target.Text != action.target || d.OwnerIdentity != (action.kind == "owner") {
				t.Fatalf("ALTER shape: %#v", d)
			}
			if len(d.Routine.Name.Parts) != 2 || d.Routine.Name.Parts[0].Text != "Quoted Schema" || d.Routine.Name.Parts[1].Text != "Old Name" || !d.Routine.SignatureSpecified || len(d.Routine.Arguments) != 2 || d.Routine.Arguments[0].Mode != "in" || d.Routine.Arguments[1].Mode != "out" {
				t.Fatal("lost exact identity syntax", d.Routine)
			}
			if !d.Routine.Arguments[0].Type.System || d.Routine.Arguments[0].Type.Name != "int4" {
				t.Fatal("utility type alias not normalized", d.Routine.Arguments[0])
			}
		}
	}
	for _, tc := range []struct {
		source    string
		specified bool
		count     int
		identity  bool
		target    string
	}{
		{"ALTER FUNCTION f RENAME TO g", false, 0, false, "g"},
		{"ALTER FUNCTION f() RENAME TO g", true, 0, false, "g"},
		{`ALTER FUNCTION f() OWNER TO "CURRENT_USER"`, true, 0, false, "CURRENT_USER"},
		{"ALTER FUNCTION f() OWNER TO CURRENT_USER", true, 0, true, "current_user"},
		{"ALTER FUNCTION f() OWNER TO SESSION_USER", true, 0, true, "session_user"},
	} {
		statements, err := Parse(tc.source)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.source, err)
		}
		d := statements[0].(*AlterRoutine)
		if d.Routine.SignatureSpecified != tc.specified || len(d.Routine.Arguments) != tc.count || d.OwnerIdentity != tc.identity || d.Target.Text != tc.target {
			t.Fatal("lost signature/owner distinction", d)
		}
	}
	for _, source := range []string{"ALTER FUNCTION f() RENAME TO g trailing", "ALTER ROUTINE f(),g() RENAME TO h", "ALTER PROCEDURE f(integer)", "ALTER FUNCTION f() SET SCHEMA", "ALTER FUNCTION f() OWNER TO"} {
		if _, err := Parse(source); err == nil {
			t.Fatal("accepted incomplete ALTER", source)
		}
	}
}
