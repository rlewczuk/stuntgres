package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestParseAlterDefaultPrivilegesOperands(t *testing.T) {
	query := `ALTER DEFAULT PRIVILEGES IN SCHEMA s,"Quoted" FOR USER owner,owner,CURRENT_ROLE GRANT SELECT(x,y),INSERT,MAINTAIN ON TABLES TO PUBLIC,GROUP reader,CURRENT_USER,SESSION_USER WITH GRANT OPTION`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s, ok := statements[0].(*AlterDefaultPrivileges)
	if !ok || s.Class != "tables" || !s.GrantOption || s.Revoke || s.OptionOnly || s.Cascade || len(s.Options) != 2 || len(s.Privileges) != 3 || len(s.Grantees) != 4 {
		t.Fatal("AST", statements[0])
	}
	if len(s.Options[0].Schemas) != 2 || s.Options[0].Schemas[0].Text != "s" || s.Options[0].Schemas[1].Text != "Quoted" || len(s.Options[1].Roles) != 3 || s.Options[1].Roles[0].Name != "owner" || s.Options[1].Roles[1].Name != "owner" || s.Options[1].Roles[2].Identity != "current" {
		t.Fatal("ordered clauses/repeated roles", s.Options)
	}
	if s.Privileges[0].Name != "select" || len(s.Privileges[0].Columns) != 2 || s.Privileges[0].Columns[0].Text != "x" || s.Privileges[0].Columns[1].Text != "y" || s.Privileges[1].Name != "insert" || s.Privileges[2].Name != "maintain" || !s.Grantees[0].Public || s.Grantees[1].Role.Name != "reader" || s.Grantees[2].Role.Identity != "current" || s.Grantees[3].Role.Identity != "session" {
		t.Fatal("operands", s)
	}
	for _, operand := range []struct {
		pos   Position
		token string
	}{{s.Options[0].Pos, "IN SCHEMA"}, {s.Options[1].Pos, "FOR USER"}, {s.Options[0].Schemas[1].Pos, `"Quoted"`}, {s.Options[1].Roles[2].Pos, "CURRENT_ROLE"}, {s.Privileges[0].Pos, "SELECT(x"}, {s.Privileges[0].Columns[0].Pos, "x,y"}, {s.Privileges[0].Columns[1].Pos, "y)"}, {s.Grantees[1].Role.Pos, "reader"}} {
		if operand.pos.Offset != strings.Index(query, operand.token) || operand.pos.Character != strings.Index(query, operand.token)+1 {
			t.Fatal("operand position", operand)
		}
	}
}

func TestParseAlterDefaultPrivilegesClassesAndFlags(t *testing.T) {
	for _, class := range []string{"TABLES", "SEQUENCES", "FUNCTIONS", "ROUTINES", "TYPES", "SCHEMAS", "LARGE OBJECTS"} {
		for _, clause := range []string{"", " FOR ROLE owner IN SCHEMA s", " IN SCHEMA s FOR USER owner"} {
			for _, suffix := range []string{"", " RESTRICT", " CASCADE"} {
				for _, prefix := range []string{"REVOKE ", "REVOKE GRANT OPTION FOR "} {
					query := "ALTER DEFAULT PRIVILEGES" + clause + " " + prefix + "ALL PRIVILEGES ON " + class + " FROM PUBLIC" + suffix
					statements, err := Parse(query)
					if err != nil || len(statements) != 1 {
						t.Fatal(query, err)
					}
					s := statements[0].(*AlterDefaultPrivileges)
					if s.Class != strings.ToLower(class) || !s.Revoke || s.OptionOnly != (prefix != "REVOKE ") || s.Cascade != (suffix == " CASCADE") || s.GrantOption || len(s.Privileges) != 1 || !s.Privileges[0].All || !s.Grantees[0].Public {
						t.Fatal("flags/class", query, s)
					}
				}
			}
		}
	}
	// Duplicate clauses survive syntax admission to preserve Execute-time checks
	// and precise second-introducer diagnostics, not a quiet Prepare side effect.
	for _, clause := range []string{"FOR ROLE a FOR USER b", "IN SCHEMA s IN SCHEMA t"} {
		statements, err := Parse("ALTER DEFAULT PRIVILEGES " + clause + " GRANT SELECT ON TABLES TO reader")
		if err != nil || len(statements) != 1 || len(statements[0].(*AlterDefaultPrivileges).Options) != 2 {
			t.Fatal("duplicate clause admission", clause, err)
		}
	}
	for _, name := range []string{`"Tables"`, `"SCHEMAS"`, `"dp_Quoted"`} {
		statements, err := Parse("ALTER DEFAULT PRIVILEGES FOR ROLE " + name + " GRANT USAGE ON TYPES TO " + name)
		if err != nil || statements[0].(*AlterDefaultPrivileges).Options[0].Roles[0].Name != strings.Trim(name, `"`) {
			t.Fatal("quoted role", name, err)
		}
	}
}

func TestParseAlterDefaultPrivilegesWholeInputBoundaries(t *testing.T) {
	cases := []struct{ query, token string }{
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLE TO reader", "TABLE"},
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES s TO reader", "s TO"},
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON ALL TABLES IN SCHEMA s TO reader", "ALL"},
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO reader GRANTED BY CURRENT_USER", "GRANTED"},
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO reader; SELECT )", ")"},
		{`ALTER DEFAULT PRIVILEGES GRANT SELECT ON "TABLES" TO reader`, `"TABLES"`},
		{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON LARGE wrong TO reader", "wrong"},
	}
	for _, tc := range cases {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != strconv.Itoa(strings.Index(tc.query, tc.token)+1) {
			t.Fatal("malformed full input", tc, statements, err)
		}
	}
}
