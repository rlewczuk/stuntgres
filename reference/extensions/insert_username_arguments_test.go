package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInsertUsernameArgumentsAndTypes(t *testing.T) {
	cases := []struct { name, definition, args, code, message string }{
		{"no_args", "actor text", "", "XX000", "insert_username (iu_bad): one argument was expected"},
		{"two_args", "actor text", "'actor','actor'", "XX000", "insert_username (iu_bad): one argument was expected"},
		{"missing", "actor text", "'missing'", "09000", `"iu_bad" has no attribute "missing"`},
		{"empty", "actor text", "''", "09000", `"iu_bad" has no attribute ""`},
		{"system", "actor text", "'ctid'", "09000", `"iu_bad" has no attribute "ctid"`},
		{"case", `"Audit User" text`, "'audit user'", "09000", `"iu_bad" has no attribute "audit user"`},
		{"literal_quotes", `"Audit User" text`, `'"Audit User"'`, "09000", `"iu_bad" has no attribute ""Audit User""`},
		{"qualified", "actor text", "'iu_bad.actor'", "09000", `"iu_bad" has no attribute "iu_bad.actor"`},
	}
	for _, typ := range []string{"integer", "varchar(20)", "char(20)", "name", "text[]", "iu_text"} {
		cases = append(cases, struct { name, definition, args, code, message string }{typ, "actor "+typ, "'actor'", "09000", `attribute "actor" of "iu_bad" must be type TEXT`})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := insertUsernameRun(t, p, b, c)
				for _, q := range []string{"CREATE EXTENSION insert_username", "CREATE DOMAIN iu_text AS text", "CREATE TABLE iu_bad(id integer,"+tc.definition+")", "CREATE TRIGGER iu_bad_stamp BEFORE INSERT ON iu_bad FOR EACH ROW EXECUTE FUNCTION insert_username("+tc.args+")"} { run(fixture.Exec(q, 0)) }
				insertUsernameBoth(t, p, b, c, insertUsernameError("INSERT INTO iu_bad(id) VALUES(1),(2)", tc.code, tc.message))
				run(fixture.Select("SELECT id FROM iu_bad ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
			})
		})
	}
	t.Run("exact_quoted_text", func(t *testing.T) {
		insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := insertUsernameRun(t, p, b, c)
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			for _, q := range []string{"CREATE ROLE iu_writer", "CREATE EXTENSION insert_username", `CREATE TABLE iu_exact(id integer,"Audit User" text NOT NULL,note text)`, "GRANT INSERT,SELECT ON iu_exact TO iu_writer", `CREATE TRIGGER iu_exact_stamp BEFORE INSERT ON iu_exact FOR EACH ROW EXECUTE FUNCTION insert_username('Audit User')`, "SET ROLE iu_writer"} { run(fixture.Exec(q, 0)) }
			run(fixture.Exec("INSERT INTO iu_exact(id) VALUES(1)", 1))
			run(fixture.Prepared(fixture.Exec(`INSERT INTO iu_exact VALUES(2,NULL,NULL)`, 1)))
			insertUsernameBoth(t, p, b, c, fixture.Select(`SELECT id,"Audit User",note FROM iu_exact ORDER BY id`, []string{"id", "Audit User", "note"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "iu_writer", nil}, {int64(2), "iu_writer", nil}}))
		})
	})
}

func TestReferenceInsertUsernameDiagnostics(t *testing.T) {
	insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := insertUsernameRun(t, p, b, c)
		missing := fixture.ImplicitError("SELECT insert_username()", fixture.Diagnostic{Code: "42883", Message: "function insert_username() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		missing.SelectRows = true
		run(missing)
		missing.ErrorStage = "prepare"
		run(fixture.Prepared(missing))
		run(fixture.Exec("CREATE EXTENSION insert_username", 0))
		s := insertUsernameError("SELECT insert_username()", "XX000", "insert_username: not fired by trigger manager")
		s.SelectRows = true
		insertUsernameBoth(t, p, b, c, s)
		wrong := fixture.ImplicitError("SELECT insert_username(1)", fixture.Diagnostic{Code: "42883", Message: "function insert_username(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		wrong.SelectRows = true
		run(wrong)
		wrong.ErrorStage = "prepare"
		run(fixture.Prepared(wrong))
		for _, q := range []string{"CREATE TABLE iu_nested(id integer)", "CREATE FUNCTION iu_nested_call() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM insert_username(); RETURN NEW; END$$", "CREATE TRIGGER iu_nested_stamp BEFORE INSERT ON iu_nested FOR EACH ROW EXECUTE FUNCTION iu_nested_call()"} { run(fixture.Exec(q, 0)) }
		// Context is an independent source-based prediction for this exact wrapper.
		d := fixture.Diagnostic{Code: "XX000", Message: "insert_username: not fired by trigger manager", Context: "SQL statement \"SELECT insert_username()\"\nPL/pgSQL function iu_nested_call() line 1 at PERFORM"}
		insertUsernameBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO iu_nested VALUES(1)", d))
		run(fixture.Select("SELECT id FROM iu_nested ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
	})
}
