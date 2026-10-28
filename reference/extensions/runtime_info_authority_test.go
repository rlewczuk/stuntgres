package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func runtimeAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	loadExec(t, p, b, c, "CREATE ROLE runtime_reader")
	loadExec(t, p, b, c, "CREATE ROLE runtime_bridge")
	defer func() {
		c.ExecContext(p.Ctx, "RESET ROLE")
		c.ExecContext(p.Ctx, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) FROM runtime_reader")
		c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) TO pg_monitor")
		c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_jit_available() TO PUBLIC")
		c.ExecContext(p.Ctx, "DROP ROLE runtime_reader")
		c.ExecContext(p.Ctx, "DROP ROLE runtime_bridge")
	}()
	query := "SELECT pg_current_logfile($1::text) AS logfile"
	stmt, err := c.PrepareContext(p.Ctx, query); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	zeroSQL := "SELECT pg_current_logfile() AS logfile"
	zeroStmt, err := c.PrepareContext(p.Ctx, zeroSQL); if err != nil { t.Fatal(err) }; defer zeroStmt.Close()
	zeroHeld := fixture.HeldStatement{Executor: c, Statement: zeroStmt, SQL: zeroSQL}
	jitSQL := "SELECT pg_jit_available() AS jit"
	jitStmt, err := c.PrepareContext(p.Ctx, jitSQL); if err != nil { t.Fatal(err) }; defer jitStmt.Close()
	jitHeld := fixture.HeldStatement{Executor: c, Statement: jitStmt, SQL: jitSQL}
	loadExec(t, p, b, c, "SET jit = off"); defer c.ExecContext(p.Ctx, "RESET jit")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	loadHeldError(t, p, b, zeroHeld, runtimeDenied("pg_current_logfile"))
	runtimeSelect(t, p, b, c, "SELECT pg_jit_available() AS jit, pg_numa_available() AS numa", []string{"jit", "numa"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}})
	runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules() WHERE module_name = 'runtime_contract_absent'", []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
	for _, arg := range []string{"", "NULL", "'stderr'", "'syslog'"} { loadError(t, p, b, c, "SELECT pg_current_logfile("+arg+")", runtimeDenied("pg_current_logfile"), false) }
	for _, value := range []any{nil, "stderr", "syslog"} {
		s := fixture.ErrorDiagnostic(query, runtimeDenied("pg_current_logfile"), value); s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_current_logfile() TO runtime_reader")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	runtimeSelect(t, p, b, c, "SELECT pg_current_logfile() AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	runtimeHeldSelect(t, p, b, zeroHeld, []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	loadError(t, p, b, c, "SELECT pg_current_logfile(NULL::text)", runtimeDenied("pg_current_logfile"), false)
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_current_logfile() FROM runtime_reader")
	loadExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_current_logfile(text) TO runtime_reader")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	for _, value := range []any{nil, "stderr"} { runtimeHeldSelect(t, p, b, held, []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}}, value) }
	invalid := fixture.ErrorDiagnostic(query, runtimeFormatError("syslog"), "syslog")
	invalid.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, invalid)
	loadError(t, p, b, c, "SELECT pg_current_logfile('syslog')", runtimeFormatError("syslog"), false)
	loadError(t, p, b, c, "SELECT pg_current_logfile()", runtimeDenied("pg_current_logfile"), false)
	loadHeldError(t, p, b, zeroHeld, runtimeDenied("pg_current_logfile"))
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(text) FROM runtime_reader")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	s := fixture.ErrorDiagnostic(query, runtimeDenied("pg_current_logfile"), nil); s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	invalid = fixture.ErrorDiagnostic(query, runtimeDenied("pg_current_logfile"), "syslog")
	invalid.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, invalid)
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "GRANT pg_monitor TO runtime_bridge")
	loadExec(t, p, b, c, "GRANT runtime_bridge TO runtime_reader")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	runtimeHeldSelect(t, p, b, held, []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}}, nil)
	runtimeSelect(t, p, b, c, "SELECT pg_current_logfile() AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) FROM pg_monitor")
	loadExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_jit_available() FROM PUBLIC")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	loadHeldError(t, p, b, jitHeld, runtimeDenied("pg_jit_available"))
	loadError(t, p, b, c, "SELECT pg_current_logfile()", runtimeDenied("pg_current_logfile"), false)
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "REVOKE pg_monitor FROM runtime_bridge")
	loadExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) TO pg_monitor")
	loadExec(t, p, b, c, "SET ROLE runtime_reader")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func TestReferenceRuntimeInfoPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"runtime_reader", "runtime_bridge"})
	loadScenario(t, runtimeAuthority)
}

func TestReferenceRuntimeInfoFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"runtime_reader", "runtime_bridge"})
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		runtimeAuthority(t, p, b, c)
		loadExec(t, p, b, c, "RESET ROLE")
		loadError(t, p, b, c, "SELECT pg_current_logfile('syslog')", runtimeFormatError("syslog"), false)
		runtimeModules(t, p, b, c)
	})
}
