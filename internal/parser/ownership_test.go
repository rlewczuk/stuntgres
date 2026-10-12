package parser

import (
	"strings"
	"testing"
)

func TestRoleOwnershipGrammar(t *testing.T) {
	for _, family := range []struct{ prefix, kind string }{
		{"ALTER TABLE public.t OWNER TO ", "table"},
		{"ALTER TABLE IF EXISTS ONLY(public.t) OWNER TO ", "table"},
		{"ALTER TYPE public.t OWNER TO ", "type"},
		{"ALTER DOMAIN public.t OWNER TO ", "domain"},
	} {
		for _, operand := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER", `"current_user"`, `"session_user"`, `"Odd Role"`} {
			q := family.prefix + operand
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			s, ok := statements[0].(*AlterOwner)
			if !ok || s.Kind != family.kind || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "public" || s.Name.Parts[1].Text != "t" {
				t.Fatalf("%s: %#v", q, statements)
			}
			identity := "current"
			if operand == "SESSION_USER" {
				identity = "session"
			}
			if operand[0] == '"' {
				identity = ""
			}
			if s.Target.Identity != identity || s.Target.Pos.Offset != len(family.prefix) || s.Pos.Offset != 0 || s.IfExists != strings.Contains(family.prefix, "IF EXISTS") {
				t.Fatalf("%s: %#v", q, s)
			}
			if identity == "" && s.Target.Name != strings.Trim(operand, `"`) {
				t.Fatal("quoted selector became identity", s.Target)
			}
		}
	}
	if _, err := Parse("CREATE TABLE reassign(reassign INT)"); err != nil {
		t.Fatal("released unreserved name category", err)
	}
}

func TestRoleOwnershipMaterializedGrammar(t *testing.T) {
	for _, prefix := range []string{"ALTER MATERIALIZED VIEW public.t OWNER TO ", "ALTER MATERIALIZED VIEW IF EXISTS public.t OWNER TO "} {
		for _, operand := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER", `"current_user"`, `"current_role"`, `"session_user"`, `"Odd Role"`} {
			statements, err := Parse(prefix + operand)
			if err != nil || len(statements) != 1 {
				t.Fatal(prefix, operand, statements, err)
			}
			stmt, ok := statements[0].(*AlterView)
			if !ok || !stmt.Materialized || stmt.Action != "owner" || stmt.IfExists != strings.Contains(prefix, "IF EXISTS") || len(stmt.Name.Parts) != 2 || stmt.Name.Parts[0].Text != "public" || stmt.Name.Parts[1].Text != "t" || stmt.Pos.Offset != 0 || stmt.Target.Pos.Offset != len(prefix) {
				t.Fatal(prefix, operand, statements)
			}
			identity := operand[0] != '"'
			name := strings.ToLower(operand)
			if !identity {
				name = strings.Trim(operand, `"`)
			}
			if stmt.OwnerIdentity != identity || stmt.Target.Text != name {
				t.Fatal("lost materialized role operand", operand, stmt)
			}
		}
	}
}
func TestReassignOwnedGrammar(t *testing.T) {
	q := `REASSIGN OWNED BY "current_user",CURRENT_ROLE,SESSION_USER TO "Odd Role"`
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s, ok := statements[0].(*ReassignOwned)
	if !ok || len(s.Sources) != 3 || s.Target.Name != "Odd Role" || s.Target.Identity != "" || s.Pos.Offset != 0 {
		t.Fatal(statements)
	}
	if s.Sources[0].Name != "current_user" || s.Sources[0].Identity != "" || s.Sources[1].Identity != "current" || s.Sources[2].Identity != "session" {
		t.Fatal(s.Sources)
	}
	if s.Target.Pos.Offset != strings.Index(q, `"Odd Role"`) {
		t.Fatal(s.Target.Pos)
	}
}
func TestDropOwnedGrammar(t *testing.T) {
	for _, mode := range []struct {
		suffix string
		want   DropMode
	}{{"", DropRestrict}, {" RESTRICT", DropRestrict}, {" CASCADE", DropCascade}} {
		statements, err := Parse("DROP OWNED BY source,source,CURRENT_USER" + mode.suffix)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		s, ok := statements[0].(*DropOwned)
		if !ok || s.Mode != mode.want || len(s.Sources) != 3 || s.Sources[0].Name != "source" || s.Sources[1].Name != "source" || s.Sources[2].Identity != "current" {
			t.Fatal(statements)
		}
	}
}
func TestRoleOwnershipWholeInput(t *testing.T) {
	for _, q := range []string{
		"ALTER TABLE t OWNER", "ALTER TABLE t OWNER TO", "ALTER TYPE t OWNER TO next EXTRA",
		"ALTER DOMAIN t OWNER TO next, OWNER TO other", "ALTER MATERIALIZED VIEW t OWNER TO next EXTRA",
		"REASSIGN OWNED BY TO next", "REASSIGN OWNED BY source, TO next", "REASSIGN OWNED BY source TO",
		"REASSIGN OWNED BY source TO next CASCADE", "DROP OWNED BY source TO next", "DROP OWNED BY source CASCADE EXTRA",
		"ALTER TABLE ONLY t OWNER TO", "ALTER TABLE ONLY(t) OWNER TO",
		"ALTER TABLE t* OWNER TO", "ALTER TABLE IF EXISTS t OWNER TO",
		"ALTER TABLE IF EXISTS ONLY t OWNER TO", "ALTER TABLE IF EXISTS ONLY(t) OWNER TO",
		"ALTER DATABASE db OWNER", "ALTER DATABASE db OWNER TO", "ALTER DATABASE db OWNER TO next EXTRA",
		"ALTER DATABASE public.db OWNER TO next", "ALTER DATABASE db, other OWNER TO next",
		"ALTER DATABASE IF EXISTS db OWNER TO next",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatalf("malformed ownership utility published syntax: %s %#v %v", q, statements, err)
		}
	}
}

func TestRoleOwnershipDatabaseGrammar(t *testing.T) {
	for _, operand := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER", `"current_user"`, `"current_role"`, `"session_user"`, `"Odd Role"`} {
		prefix := `ALTER DATABASE "Odd DB" OWNER TO `
		statements, err := Parse(prefix + operand)
		if err != nil || len(statements) != 1 {
			t.Fatal(operand, statements, err)
		}
		stmt, ok := statements[0].(*AlterOwner)
		if !ok || stmt.Kind != "database" || stmt.IfExists || len(stmt.Name.Parts) != 1 || stmt.Name.Parts[0].Text != "Odd DB" || stmt.Name.Parts[0].Pos.Offset != len("ALTER DATABASE ") || stmt.Pos.Offset != 0 || stmt.Target.Pos.Offset != len(prefix) {
			t.Fatal(operand, statements)
		}
		identity := "current"
		if operand == "SESSION_USER" {
			identity = "session"
		}
		if operand[0] == '"' {
			identity = ""
		}
		if stmt.Target.Identity != identity || identity == "" && stmt.Target.Name != strings.Trim(operand, `"`) {
			t.Fatal(operand, stmt.Target)
		}
	}
}
