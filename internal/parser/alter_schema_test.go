package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterSchemaSyntax(t *testing.T) {
	for _, q := range []string{
		"ALTER TABLE t SET SCHEMA s2", "ALTER TABLE s1.t SET SCHEMA s2",
		"ALTER TABLE IF EXISTS s1.t SET SCHEMA s2", "ALTER TABLE ONLY t SET SCHEMA s2",
		"ALTER TABLE ONLY(t) SET SCHEMA s2", "ALTER TABLE t* SET SCHEMA s2",
		"ALTER TABLE IF EXISTS ONLY t SET SCHEMA s2",
		"ALTER TABLE IF EXISTS ONLY(t) SET SCHEMA s2",
		"ALTER TABLE IF EXISTS t* SET SCHEMA s2",
		`ALTER TABLE "Odd.Table" SET SCHEMA "Mixed.Schema"`,
		"ALTER TABLE if.t SET SCHEMA if", "ALTER TABLE IF EXISTS if SET SCHEMA if",
		`ALTER TABLE t SET SCHEMA "` + strings.Repeat("界", 22) + `"`,
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatalf("admission: statements=%v error=%v", statements, err)
			}
			s, ok := statements[0].(*AlterTableSetSchema)
			if !ok || s.Pos.Character != 1 || len(s.Name.Parts) == 0 || s.Schema.Text == "" {
				t.Fatal("concrete SET SCHEMA AST", statements)
			}
			if s.IfExists != strings.Contains(q, "IF EXISTS") || s.Only != strings.Contains(q, "ONLY") {
				t.Fatal("target markers", s)
			}
			if strings.Contains(q, "界") && (s.Schema.Text != strings.Repeat("界", 21) || s.Schema.Pos.Character != 26) {
				t.Fatal("identifier normalization/position", s.Schema)
			}
		})
	}
}

func TestAlterSchemaWholeInput(t *testing.T) {
	for _, q := range []string{
		"ALTER TABLE t SET SCHEMA", "ALTER TABLE t SET SCHEMA 's2'",
		"ALTER TABLE t SET SCHEMA $1", "ALTER TABLE t SET SCHEMA s2.extra",
		"ALTER TABLE t SET SCHEMA s2 RESTRICT",
		"ALTER TABLE s1.t SET SCHEMA s2, DROP COLUMN missing",
		"ALTER TABLE IF EXISTS missing SET SCHEMA s2, DROP COLUMN missing",
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse("CREATE TABLE prefix(a INT);" + q)
			var diagnostic *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
				t.Fatalf("full input exposed statements=%v diagnostic=%#v error=%v", statements, diagnostic, err)
			}
		})
	}
	_, err := Parse("ALTER TABLE s1.t SET SCHEMA s2, DROP COLUMN missing")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ","`, Position: "31"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic=%#v error=%v want=%#v", got, err, want)
	}
}

func TestAlterSchemaUnsupportedActionGuard(t *testing.T) {
	for _, target := range []string{"t", "s1.t", "ONLY t", "ONLY(t)", "t*", "IF EXISTS t", "IF EXISTS ONLY(t)"} {
		for _, action := range []string{"SET", "OWNER"} {
			q := "ALTER TABLE " + target + " " + action
			statements, err := Parse(q)
			var got *sqlstate.Error
			want := sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000",
				Message:  "ALTER TABLE action is unsupported",
				Position: strconv.Itoa(len("ALTER TABLE "+target+" ") + 1),
			}
			if len(statements) != 0 || !errors.As(err, &got) || *got != want {
				t.Fatalf("%s: statements=%v diagnostic=%#v error=%v want=%#v", q, statements, got, err, want)
			}
		}
	}
}
