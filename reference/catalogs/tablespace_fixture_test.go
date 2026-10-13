package reference_test

import (
	"database/sql"
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const tablespaceAbsent = "/tmp/stuntgres_tablespace_contract_absent/location"
const tablespacePhysicalMessage = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

func tablespaceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				tablespaceContainer(t, p, "test ! -e /tmp/stuntgres_tablespace_contract_absent")
			}
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			body(t, p, b, c)
		})
	}
}

func tablespaceContainer(t *testing.T, p *fixture.Pair, script string) {
	t.Helper()
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("tablespace container setup exit %d: %s", code, body)
	}
}

func tablespaceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
}

func tablespaceDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func tablespaceError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want fixture.Diagnostic, parse bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want)
		s.Prepared, s.ErrorStage = prepared, "execute"
		if prepared && parse {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func tablespacePhysical(b fixture.Backend, path string) fixture.Diagnostic {
	if b.PG {
		return tablespaceDiagnostic("58P01", "directory \""+path+"\" does not exist")
	}
	return tablespaceDiagnostic("0A000", tablespacePhysicalMessage)
}

func tablespaceSQL(name, owner, path string) string {
	if owner != "" {
		owner = " OWNER " + owner
	}
	return "CREATE TABLESPACE " + name + owner + " LOCATION '" + strings.ReplaceAll(path, "'", "''") + "'"
}

func tablespaceBuiltins(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	// pq exposes OID bytes (normalized to strings); the native OID carrier is int64.
	rows := [][]any{{int64(1663), "pg_default", int64(10), nil}, {int64(1664), "pg_global", int64(10), nil}}
	if b.PG {
		rows = [][]any{{"1663", "pg_default", "10", nil}, {"1664", "pg_global", "10", nil}}
	}
	s := fixture.Select("SELECT oid,spcname,spcowner,spcoptions FROM pg_catalog.pg_tablespace WHERE oid=1663 OR oid=1664 ORDER BY oid", []string{"oid", "spcname", "spcowner", "spcoptions"}, []string{"OID", "NAME", "OID", "_TEXT"}, rows)
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func tablespaceNoUserRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT spcname FROM pg_tablespace WHERE spcname LIKE 'ts_contract_%' ORDER BY spcname", []string{"spcname"}, []string{"NAME"}, [][]any{}))
}

func tablespaceRole(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, attributes string) {
	t.Helper()
	tablespaceExec(t, p, b, c, "CREATE ROLE "+name+" "+attributes)
	t.Cleanup(func() {
		if _, err := b.DB.ExecContext(p.Ctx, "DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
}
