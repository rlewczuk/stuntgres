package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGLargeobjectControls(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		var owner int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgl_owner'").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_owner", 0))
		run(pglCreate(920101, strings.Repeat("00ff", 1025)))
		run(pglValue("lo_create(920102::oid)", "OID", int64(920102)))
		run(pglCreate(920103, "6162"))
		run(pglPut(920103, 4097, "dead00ff"))
		run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 920102 FROM pgl_owner", 0))
		run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 920103 TO pgl_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 920103 FROM pgl_owner", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		verify := func() {
			run(pglPages(920101, 0, bytes.Repeat([]byte{0, 255}, 1024), 1, []byte{0, 255}))
			run(pglPages(920102))
			run(pglPages(920103, 0, []byte("ab"), 2, []byte{0, 222, 173, 0, 255}))
			run(fixture.Select("SELECT oid,lomowner,lomacl FROM pg_largeobject_metadata WHERE oid IN (920101,920102,920103) ORDER BY oid", []string{"oid", "lomowner", "lomacl"}, []string{"OID", "OID", "_ACLITEM"}, [][]any{{int64(920101), owner, nil}, {int64(920102), owner, "{}"}, {int64(920103), owner, "{pgl_reader=r*w*/pgl_owner}"}}))
		}
		verify()
		if b.PG {
			return
		}
		run(fixture.Exec("BEGIN", 0))
		run(pglPut(920101, 0, "ffff"))
		run(pglCreate(920104, "ff"))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ROLLBACK", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(pglPages(920101))
		run(pglPages(920103))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify()
		run(pglPages(920104))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		run(pglError("SELECT * FROM pg_largeobject LIMIT 0", "42501", "permission denied for table pg_largeobject"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(pglCreate(920105, "5a00"))
		invalid := dump.String() + "\nSELECT pg_catalog.lo_put(920101::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('aabb','hex'));\nSELECT pg_catalog.lo_get(920101::pg_catalog.oid);"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid)); err == nil {
			t.Fatal("late invalid Restore succeeded")
		}
		verify()
		run(pglPages(920105, 0, []byte{90, 0}))
	})
}

func TestReferencePGLargeobjectSmoke(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("BEGIN", 0))
		run(pglCreate(920101, strings.Repeat("00ff", 1025)))
		run(pglPages(920101, 0, bytes.Repeat([]byte{0, 255}, 1024), 1, []byte{0, 255}))
		run(pglValue("lo_create(920102::oid)", "OID", int64(920102)))
		run(pglPages(920102))
		run(pglPut(920102, 4097, "dead00ff"))
		run(pglPages(920102, 2, []byte{0, 222, 173, 0, 255}))
		run(fixture.Exec("ROLLBACK", 0))
		run(pglPages(920101))
		run(pglPages(920102))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgl_reader", 0))
		run(pglError("SELECT * FROM pg_largeobject WHERE FALSE", "42501", "permission denied for table pg_largeobject"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
