package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fileFDWDenied(query, option string) fixture.Step {
	role := "pg_read_server_files"
	if option == "program" {
		role = "pg_execute_server_program"
	}
	d := fdwDiagnostic("42501", `permission denied to set the "`+option+`" option of a file_fdw foreign table`)
	d.Detail = `Only roles with privileges of the "` + role + `" role may set this option.`
	s := fixture.ErrorDiagnostic(query, d)
	s.ErrorStage = "execute"
	return s
}

func TestReferenceFileFDWPrivileges(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		for _, role := range []string{"ff_actor", "ff_middle", "ff_noinherit", "ff_file", "ff_program_role"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("GRANT CREATE,USAGE ON SCHEMA public TO ff_actor,ff_noinherit,ff_file,ff_program_role", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ff_server TO ff_actor,ff_noinherit,ff_file,ff_program_role", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_table(id integer,txt text) SERVER ff_server OPTIONS(filename '/x',format 'csv')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OWNER TO ff_actor", 0))
		q := "SELECT file_fdw_validator(ARRAY[$1::text],3118::oid) AS validated"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		heldRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		run(fixture.Exec("SET ROLE ff_actor", 0))
		for _, option := range []string{"filename", "program"} {
			run(fileFDWDenied("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server OPTIONS("+option+" '/x',format 'INVALID')", option))
			fileFDWAbsent(run)
			s := fileFDWDenied(q, option)
			s.Args = []any{option + "=/x"}
			heldRun(s)
		}
		run(fileFDWDenied("ALTER FOREIGN TABLE ff_table OPTIONS(SET format 'text')", "filename"))
		run(fileFDWOptions("ff_table", "filename=/x", "format=csv"))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table ALTER COLUMN txt OPTIONS(ADD force_null 'true')", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT pg_read_server_files TO ff_middle", 0))
		run(fixture.Exec("GRANT ff_middle TO ff_actor", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		heldRun(fileFDWVoid(q, "filename=/x"))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_empty(id integer) SERVER ff_server OPTIONS(filename '')", 0))
		run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='ff_empty'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"ff_actor"}}))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OPTIONS(SET format 'text')", 0))
		run(fileFDWDenied("ALTER FOREIGN TABLE ff_table OPTIONS(DROP filename,ADD program 'not-a-real-filefdw-program')", "program"))
		run(fileFDWOptions("ff_table", "filename=/x", "format=text"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE ff_middle FROM ff_actor", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		s := fileFDWDenied(q, "filename")
		s.Args = []any{"filename=/x"}
		heldRun(s)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT pg_read_server_files TO ff_noinherit WITH INHERIT FALSE", 0))
		run(fixture.Exec("SET ROLE ff_noinherit", 0))
		run(fileFDWDenied(fileFDWCall([]string{"filename=/x"}, 3118), "filename"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT pg_read_server_files TO ff_file", 0))
		run(fixture.Exec("GRANT pg_execute_server_program TO ff_program_role", 0))
		for _, tc := range []struct{ role, allowed, denied string }{{"ff_file", "filename", "program"}, {"ff_program_role", "program", "filename"}} {
			run(fixture.Exec("SET ROLE "+tc.role, 0))
			heldRun(fileFDWVoid(q, tc.allowed+"=/x"))
			s := fileFDWDenied(q, tc.denied)
			s.Args = []any{tc.denied + "=/x"}
			heldRun(s)
			run(fixture.Exec("RESET ROLE", 0))
		}
		// Authority granted inside a rolled-back savepoint must not survive in
		// the already prepared executable or publish its foreign declaration.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ff_privilege", 0))
		run(fixture.Exec("GRANT ff_middle TO ff_actor", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		heldRun(fileFDWVoid(q, "filename=/x"))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server OPTIONS(filename '/x')", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ff_privilege", 0))
		fileFDWAbsent(run)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		s = fileFDWDenied(q, "filename")
		s.Args = []any{"filename=/x"}
		heldRun(s)
		run(fixture.Exec("RESET ROLE", 0))
		heldRun(fileFDWVoid(q, "program=not-a-real-filefdw-program"))
	})
}

func TestReferenceFileFDWAtomicity(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		run(fileFDWError("CREATE FOREIGN TABLE ff_rejected(id integer,id integer) SERVER ff_server OPTIONS(filename '/x')", "42701", `column "id" specified more than once`))
		fileFDWAbsent(run)
		run(fixture.Exec("CREATE ROLE ff_owner", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ff_server TO ff_owner", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_table(id integer,txt text OPTIONS(force_null 'true')) SERVER ff_server OPTIONS(filename '/x',format 'csv')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OWNER TO ff_owner", 0))
		run(fixture.Exec("CREATE TABLE ff_local(id integer,txt text)", 0))
		run(fixture.Exec("INSERT INTO ff_local VALUES(7,'kept'),(8,NULL)", 2))
		state := func() {
			run(fileFDWOptions("ff_table", "filename=/x", "format=csv", "null=NIL", "header=match"))
			run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='ff_table'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"ff_owner"}}))
			run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"id", int64(1), nil}, {"txt", int64(2), "{force_null=true}"}}))
			run(fixture.Select("SELECT id,txt FROM ff_local ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), nil}}))
		}
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OPTIONS(ADD header 'true',ADD null 'NIL')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OPTIONS(DROP header,ADD header 'match')", 0))
		state()
		for _, tc := range []struct{ query, code, primary string }{
			{"ALTER FOREIGN TABLE ff_table OPTIONS(ADD format 'text')", "42710", `option "format" provided more than once`},
			{"ALTER FOREIGN TABLE ff_table OPTIONS(SET absent 'x')", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE ff_table OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE ff_table ADD COLUMN earlier integer,OPTIONS(SET format 'INVALID')", "22023", `COPY format "INVALID" not recognized`},
			{"ALTER FOREIGN TABLE ff_table OPTIONS(SET filename '/changed'),ALTER COLUMN txt OPTIONS(ADD force_not_null '1')", "42601", "force_not_null requires a Boolean value"},
			{"ALTER FOREIGN TABLE ff_table OPTIONS(DROP filename)", "HV002", "either filename or program is required for file_fdw foreign tables"},
		} {
			run(fileFDWError(tc.query, tc.code, tc.primary))
			state()
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ff_state", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OWNER TO CURRENT_USER", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OPTIONS(SET filename '/temporary')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server OPTIONS(filename '/temporary')", 0))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ff_state", 0))
		state()
		fileFDWAbsent(run)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table RENAME COLUMN txt TO renamed", 0))
		run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_table'::regclass AND attname='renamed'", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"renamed", int64(2), "{force_null=true}"}}))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table DROP COLUMN renamed", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table ADD COLUMN txt text OPTIONS(force_not_null 'on')", 0))
		run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"id", int64(1), nil}, {"txt", int64(3), "{force_not_null=on}"}}))
	})
}
