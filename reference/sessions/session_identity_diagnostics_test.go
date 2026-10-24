package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityFormDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Cleanup(func() {
			if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS identity_empty CASCADE"); err != nil {
				t.Error("identity diagnostic schema cleanup", err)
			}
		})
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE identity_reference_t(id INT)", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE identity_t(id INT,who TEXT)", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE SCHEMA identity_empty", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, code, message, pos, hint string }{
				{"SELECT current_user()", "42601", `syntax error at or near "("`, "20", ""},
				{"SELECT current_catalog()", "42601", `syntax error at or near "("`, "23", ""},
				{"SELECT current_role()", "42601", `syntax error at or near "("`, "20", ""},
				{"SELECT session_user()", "42601", `syntax error at or near "("`, "20", ""},
				{"SELECT user()", "42601", `syntax error at or near "("`, "12", ""},
				{"SELECT system_user()", "42601", `syntax error at or near "("`, "19", ""},
				{"SELECT current_database(1)", "42883", "function current_database(integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT current_database('x')", "42883", "function current_database(unknown) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT current_database(1::BIGINT)", "42883", "function current_database(bigint) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT current_database(1,2)", "42883", "function current_database(integer, integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT current_database(VARIADIC 1)", "42883", "function current_database(integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT pg_catalog.current_catalog()", "42883", "function pg_catalog.current_catalog() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT pg_catalog.user()", "42883", "function pg_catalog.user() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{`SELECT "CURRENT_DATABASE"()`, "42883", "function CURRENT_DATABASE() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT pg_catalog.current_role()", "42883", "function pg_catalog.current_role() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SET ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "10", ""},
				{"SET SESSION ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "18", ""},
				{`SELECT "current_user" FROM identity_reference_t`, "42703", `column "current_user" does not exist`, "8", ""},
				{"SELECT current_user+1 FROM identity_reference_t", "42883", "operator does not exist: name + integer", "20", "No operator matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT id FROM identity_reference_t WHERE current_user", "42804", "argument of WHERE must be type boolean, not type name", "43", ""},
				{"SELECT missing_identity_schema.current_database()", "3F000", `schema "missing_identity_schema" does not exist`, "8", ""},
				{"/* é */ SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "16", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT identity_empty.current_database()", "42883", "function identity_empty.current_database() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{`SELECT "current_user" FROM identity_t`, "42703", `column "current_user" does not exist`, "8", ""},
				{"SELECT current_user+1 FROM identity_t", "42883", "operator does not exist: name + integer", "20", "No operator matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT id FROM identity_t WHERE current_user", "42804", "argument of WHERE must be type boolean, not type name", "33", ""},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
		}
		account := "postgres"
		if backend.PG {
			account = "stuntgres"
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT CAST(current_user AS TEXT)", []string{"current_user"}, []string{"TEXT"}, [][]any{{account}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
		}
	}
}
