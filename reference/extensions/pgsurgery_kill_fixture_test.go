package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const killSQL = "SELECT ks_part.heap_force_kill($1::regclass,$2::tid[]) AS killed"

// ksCase shares freeze's fresh backend lifecycle, not its operation or schema.
func ksCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ks_part")
	fixture.SchemaReferenceCleanup(t, "ks_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := freezeConn(t, p, b)
			defer c.Close()
			if b.PG {
				clean := func() {
					c.ExecContext(p.Ctx, "ROLLBACK")
					c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					cleaner := freezeConn(t, p, b)
					defer cleaner.Close()
					for _, q := range []string{"DROP EXTENSION IF EXISTS pg_surgery CASCADE", "DROP SCHEMA IF EXISTS ks_part CASCADE", "DROP SCHEMA IF EXISTS ks_moved CASCADE"} {
						if _, err := cleaner.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{
				"CREATE SCHEMA ks_part", "CREATE EXTENSION pg_surgery SCHEMA ks_part",
				"CREATE TABLE ks_part.heap(k integer,v text)",
				"ALTER TABLE ks_part.heap SET(autovacuum_enabled=false)",
			} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func ksVoid(q string, args ...any) fixture.Step {
	return fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, args...)
}

func ksError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	return s
}

func ksRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expected ...freezeHeader) {
	t.Helper()
	var values [][]any
	for _, h := range expected {
		values = append(values, []any{h.k, h.v, h.tid, h.xmin, h.xmax})
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,ctid,xmin,xmax FROM ks_part.heap ORDER BY k,v", []string{"k", "v", "ctid", "xmin", "xmax"}, []string{"INT4", "TEXT", "TID", "XID", "XID"}, values))
}

// A real ordinary DELETE control proves this trigger executes; kill must bypass it.
// The same supported plpgsql trigger setup is used by freeze producer witnesses.
func ksAuditSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE ks_part.audit(k integer)",
		`CREATE FUNCTION ks_part.delete_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO ks_part.audit VALUES(OLD.k);
  RETURN NULL;
END$$`,
		"CREATE TRIGGER kill_delete_audit AFTER DELETE ON ks_part.heap FOR EACH ROW EXECUTE FUNCTION ks_part.delete_audit()",
	} {
		freezeRun(t, p, b, c, fixture.Exec(q, 0))
	}
}
