package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGLargeobjectAuthorization(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_owner", 0))
		run(pglCreate(920101, "0080ff"))
		run(pglValue("encode(lo_get(920101::oid),'hex')", "TEXT", "0080ff"))
		denied := func(q string) { run(pglError(q, "42501", "permission denied for table pg_largeobject")) }
		denied("SELECT * FROM pg_largeobject WHERE FALSE")
		denied("SELECT * FROM pg_largeobject LIMIT 0")
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 920101 TO pgl_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		run(pglValue("encode(lo_get(920101::oid),'hex')", "TEXT", "0080ff"))
		denied("SELECT data FROM pg_largeobject WHERE loid=920101::oid")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_largeobject TO pgl_reader", 0))
		run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 920101 FROM pgl_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		run(pglPages(920101, 0, []byte{0, 128, 255}))
		run(pglError("SELECT lo_get(920101::oid)", "42501", "permission denied for large object 920101"))
		run(pglError("SELECT lo_put(920101::oid,0::bigint,decode('aa','hex'))", "42501", "permission denied for large object 920101"))
		run(pglError("SELECT lo_unlink(920101::oid)", "42501", "must be owner of large object 920101"))
		q := "SELECT loid,pageno,data FROM pg_largeobject WHERE loid=$1 ORDER BY pageno"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(wantError bool) {
			s := pglPages(920101, 0, []byte{0, 128, 255})
			s.Query = q
			s.Args = []any{int64(920101)}
			if wantError {
				s = pglError(q, "42501", "permission denied for table pg_largeobject")
				s.Args = []any{int64(920101)}
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, pglBackendStep(s, b))
		}
		check(false)
		rows, err := held.QueryContext(p.Ctx, int64(920101))
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing admitted page", rows.Err())
		}
		var oid int64
		var page int
		var saved []byte
		if err := rows.Scan(&oid, &page, &saved); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 920101 TO pgl_reader", 0))
		run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 920101 FROM pgl_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		check(false)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_largeobject FROM pgl_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		check(true)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(loid,pageno) ON pg_catalog.pg_largeobject TO pgl_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		run(fixture.Select("SELECT loid,pageno FROM pg_largeobject WHERE loid=920101::oid", []string{"loid", "pageno"}, []string{"OID", "INT4"}, [][]any{{int64(920101), int64(0)}}))
		denied("SELECT data FROM pg_largeobject WHERE loid=920101::oid")
		check(true)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_largeobject TO pgl_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		check(false)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_owner", 0))
		check(true)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(pglPages(920101, 0, []byte{0, 128, 255}))
		if oid != 920101 || page != 0 || !bytes.Equal(saved, []byte{0, 128, 255}) {
			t.Fatal("admitted owned buffer changed", oid, page, saved)
		}
	})
}

func TestReferencePGLargeobjectDiagnostics(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(pglCreate(920101, "00ff"))
		run(pglError("SELECT lo_get(920120::oid)", "42704", "large object 920120 does not exist"))
		run(pglError("SELECT lo_put(920120::oid,0::bigint,decode('ff','hex'))", "42704", "large object 920120 does not exist"))
		run(pglPages(920101, 0, []byte{0, 255}))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(pglError("SELECT lo_put(920101::oid,0::bigint,decode('ff','hex'))", "25006", "cannot execute lo_put() in a read-only transaction"))
		run(fixture.Exec("ROLLBACK", 0))
		run(pglPages(920101, 0, []byte{0, 255}))
	})
}
