package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func beforeStatementAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bst_actor"})
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE ROLE bst_actor", "GRANT USAGE ON SCHEMA public TO bst_actor", "GRANT INSERT,SELECT ON bst TO bst_actor", "GRANT INSERT,SELECT ON bst_log TO bst_actor", "GRANT USAGE ON SEQUENCE bst_log_seq_seq TO bst_actor", "REVOKE EXECUTE ON FUNCTION bst_log() FROM PUBLIC", "SET SESSION AUTHORIZATION bst_actor")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "CREATE TRIGGER s BEFORE INSERT ON bst EXECUTE FUNCTION bst_log()"
		// Invalid DDL is intentionally not prepared: rejection may be at Parse.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table bst")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT TRIGGER ON bst TO bst_actor", "SET SESSION AUTHORIZATION bst_actor")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function bst_log")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bst_log() TO bst_actor", "SET SESSION AUTHORIZATION bst_actor", q, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bst_log() FROM bst_actor", "SET SESSION AUTHORIZATION bst_actor")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 1))
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 2))
		run(beforeStatementCount("bst_log", 2))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE INSERT ON bst FROM bst_actor", "SET SESSION AUTHORIZATION bst_actor")
		run(fixture.ErrorDiagnostic("INSERT INTO bst(id,v) VALUES(3,30)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table bst")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(beforeStatementCount("bst", 2))
		run(beforeStatementCount("bst_log", 2))
		userOperatorExec(t, p, b, c, "DROP OWNED BY bst_actor", "DROP ROLE bst_actor")
	})
}

func beforeStatementModeOwnership(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE bst_switch(id integer)")
		run(fixture.Exec("INSERT INTO bst_switch VALUES(1)", 1))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_switch() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('session_replication_role','origin',false); RETURN NULL; END$$", "CREATE TRIGGER s BEFORE UPDATE ON bst_switch EXECUTE FUNCTION bst_switch()", "ALTER TABLE bst_switch ENABLE ALWAYS TRIGGER s", "CREATE TRIGGER s BEFORE INSERT ON bst EXECUTE FUNCTION bst_log()")
		defer c.ExecContext(p.Ctx, "SET session_replication_role=origin")
		q := "WITH switched AS (UPDATE bst_switch SET id=id RETURNING id), x AS (INSERT INTO bst(id,v) SELECT id+$1,10 FROM switched RETURNING id) INSERT INTO bst(id,v) SELECT id+1,20 FROM x"
		for _, offset := range []int{0, 10} {
			userOperatorExec(t, p, b, c, "SET session_replication_role=replica")
			run(fixture.Exec(q, 1, offset))
			run(beforeStatementCount("bst_log", 0))
		}
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(100,10)", 1))
		run(beforeStatementLog([]any{"s", "INSERT", int64(4)}))
	})
}

func beforeStatementRowBoundary(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		beforeStatementChain(t, p, b, c, "INSERT")
		userOperatorExec(t, p, b, c, "CREATE TABLE bst_rowlog(id integer,n bigint)", "CREATE FUNCTION bst_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_rowlog VALUES(NEW.id,(SELECT count(*) FROM bst_log)); NEW.v:=NEW.v+1; RETURN CASE WHEN NEW.id=3 THEN NULL ELSE NEW END; END$$", "CREATE TRIGGER r BEFORE INSERT ON bst FOR EACH ROW EXECUTE FUNCTION bst_row()")
		q := "INSERT INTO bst(id,v) VALUES($1,10),($2,20) RETURNING id,v"
		s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}, 1, 2)
		s.Bag = true
		run(s)
		run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(21)}}, 3, 4))
		run(fixture.Select("SELECT id,n FROM bst_rowlog ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}, {int64(3), int64(4)}, {int64(4), int64(4)}}))
		run(beforeStatementLog([]any{"a", "INSERT", int64(0)}, []any{"z", "INSERT", int64(0)}, []any{"a", "INSERT", int64(2)}, []any{"z", "INSERT", int64(2)}))
	})
}
