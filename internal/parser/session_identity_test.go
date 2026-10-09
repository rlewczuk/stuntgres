package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSessionIdentityScalarForms(t *testing.T) {
	for _, q := range []string{
		"SELECT current_catalog=current_database(), current_role=current_user, session_user=user",
		"SELECT current_user AS who", "SELECT current_user+1", "SELECT current_user FROM t WHERE who=current_user",
		"SELECT pg_catalog.current_user()", `SELECT "current_user"()`, `SELECT "CURRENT_DATABASE"()`,
		"SELECT CAST(current_user AS TEXT)", "SELECT COALESCE(system_user,'trusted')",
		"CREATE TABLE t(who TEXT DEFAULT current_user)", "CREATE VIEW v AS SELECT current_user FROM t",
		"SET ROLE NONE", "SET SESSION ROLE 'none'", "SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", "RESET ROLE",
		"CREATE TABLE current_database(id INT)", "INSERT INTO current_database(id) VALUES(1)",
		`CREATE TABLE "current_user"(id INT)`, "CREATE TABLE public.user(id INT)",
		"CREATE TABLE public.current_schema(id INT)", "SELECT current_schema()", "SELECT pg_catalog.current_schema()",
	} {
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 1 {
			t.Fatal(q, stmts, err)
		}
	}
	for _, q := range []string{"SELECT 1", "SELECT 1+1", "SELECT x", "SELECT current_user,x"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
			t.Fatal(q, statements, err)
		}
	}
	for _, name := range []string{"current_catalog", "current_role", "current_user", "session_user", "user", "system_user"} {
		_, err := Parse("SELECT " + name + "()")
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near "("` {
			t.Fatal(name, err)
		}
	}
}
