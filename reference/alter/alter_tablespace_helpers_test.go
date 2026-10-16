package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func alterTablespaceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ats_owner", "ats_reader", "ats_member", "ats_other", "ats_live", "current_user"})
	fixture.SchemaReferenceCleanup(t, "ats_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			t.Run("scenario", func(t *testing.T) { body(t, p, b, c) })
		})
	}
}

func atsRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func atsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string) {
	t.Helper()
	atsRun(t, p, b, e, fixture.Exec(q, 0))
}
func atsDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}
func atsError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		atsRun(t, p, b, e, s)
	}
}
func atsSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	atsExec(t, p, b, c, "CREATE TABLE t(id INT CONSTRAINT ats_nn NOT NULL,note TEXT DEFAULT 'fallback')")
	atsRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(7,'keep')", 1))
	atsExec(t, p, b, c, "COMMENT ON TABLE t IS 'placement witness'")
	atsExec(t, p, b, c, "CREATE INDEX ats_idx ON t(id)")
}
func atsRows() fixture.Step {
	return fixture.Select("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}})
}
func atsPlacement(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, want int64) {
	t.Helper()
	atsRun(t, p, b, c, fixture.Select("SELECT reltablespace::bigint FROM pg_class WHERE oid='"+name+"'::regclass::oid", []string{"reltablespace"}, []string{"INT8"}, [][]any{{want}}))
}
func atsRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, role := range []string{"ats_owner", "ats_reader", "ats_member", "ats_other"} {
		atsExec(t, p, b, c, "CREATE ROLE "+role)
	}
	atsExec(t, p, b, c, "GRANT USAGE,CREATE ON SCHEMA public TO ats_owner,ats_reader,ats_member,ats_other")
	atsExec(t, p, b, c, "GRANT ats_owner TO ats_member")
}
