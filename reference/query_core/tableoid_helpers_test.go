package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func tableoidIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT c.oid::bigint FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing independent pg_class identity", name, rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 4294967295 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid or nonunique relation identity", name, oid, rows.Err())
	}
	return oid
}

func tableoidCell(b fixture.Backend, oid int64) any {
	if b.PG {
		return strconv.FormatInt(oid, 10)
	}
	return oid
}

func tableoidRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func tableoidDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
