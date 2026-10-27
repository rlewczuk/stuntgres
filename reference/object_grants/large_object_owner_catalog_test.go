package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectOwnerMetadata(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910201, 910202)
		old := largeObjectOwnerOID(t, p, c, "lo_old")
		newOwner := largeObjectOwnerOID(t, p, c, "lo_new")
		largeObjectOwnerCreate(run, 910201)
		fixture.RoleGrantActor(run, "lo_old", func() {
			run(largeObjectPut(910201, 2047, "aabb00"))
			run(largeObjectPut(910201, 4294967296, "00ff00"))
			run(largeObjectValue("lo_create(910202::oid)", "OID", int64(910202)))
		})
		pages := func() {
			run(fixture.Select("SELECT loid,pageno,encode(data,'hex') AS data FROM pg_largeobject WHERE loid IN (910201,910202) ORDER BY loid,pageno", []string{"loid", "pageno", "data"}, []string{"OID", "INT4", "TEXT"}, [][]any{
				{int64(910201), int64(0), "00017f80ff00" + stringZeros(2041) + "aa"},
				{int64(910201), int64(1), "bb00"},
				{int64(910201), int64(2097152), "00ff00"},
			}))
			// Assert the physical data column too, without normalizing BYTEA to hex.
			run(fixture.Select("SELECT pageno,data FROM pg_largeobject WHERE loid=910201::oid AND pageno=1", []string{"pageno", "data"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), string([]byte{0xbb, 0})}}))
		}
		largeObjectOwnerMetadata(run, 910201, old, nil)
		pages()
		q := largeObjectOwnerAlter(910201, "lo_new")
		held := largeObjectOwnerHeld(t, p, b, c, q)
		// Preparation has no catalog effect; utility Query has zero result columns.
		largeObjectOwnerMetadata(run, 910201, old, nil)
		// Recipient defaults are creation policy, not ownership-transfer ACLs.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE lo_new GRANT SELECT ON LARGE OBJECTS TO lo_reader", 0))
		defer func() {
			c.ExecContext(p.Ctx, "ROLLBACK")
			c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE lo_new REVOKE SELECT ON LARGE OBJECTS FROM lo_reader"); err != nil { t.Error(err) }
		}()
		held(fixture.Exec(q, 0))
		run(fixture.Exec(largeObjectOwnerAlter(910202, "lo_new"), 0))
		largeObjectOwnerMetadata(run, 910201, newOwner, nil)
		var retained []*sql.ColumnType
		shape := largeObjectMetadataStep(910201, [][]any{{int64(910201), newOwner, nil}})
		shape.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			var err error
			retained, err = rows.ColumnTypes()
			if err != nil { t.Fatal(err) }
			largeObjectMetadataColumns(t, retained, b.PG)
		}
		run(shape)
		largeObjectMetadataColumns(t, retained, b.PG)
		largeObjectOwnerMetadata(run, 910202, newOwner, nil)
		run(largeObjectValue("pg_get_acl(2995,910201::oid,0)", "_ACLITEM", nil))
		pages()
		run(largeObjectValue("encode(lo_get(910201::oid,0::bigint,6),'hex')", "TEXT", "00017f80ff00"))
		run(largeObjectValue("encode(lo_get(910201::oid,2046::bigint,6),'hex')", "TEXT", "00aabb000000"))
		run(largeObjectValue("encode(lo_get(910201::oid,4294967294::bigint,8),'hex')", "TEXT", "000000ff00"))
		run(largeObjectBytes(910202, ""))
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || len(columns) != 0 || rows.Next() { t.Fatalf("utility columns=%v err=%v", columns, err) }
		if err := rows.Err(); err != nil { t.Fatal(err) }
	})
}

func stringZeros(n int) string {
	// Physical page expectations include every zero byte, not only fragments.
	out := make([]byte, n*2)
	for i := range out { out[i] = '0' }
	return string(out)
}

func TestReferenceLargeObjectOwnerACL(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910211, 910212, 910213, 910214)
		newOwner := largeObjectOwnerOID(t, p, c, "lo_new")
		for _, oid := range []uint32{910211, 910212, 910213, 910214} { largeObjectOwnerCreate(run, oid) }
		fixture.RoleGrantActor(run, "lo_old", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910212 FROM lo_old", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910213 TO lo_new WITH GRANT OPTION", 0))
			run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910213 TO lo_reader", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910213 TO PUBLIC", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910214 TO lo_reader WITH GRANT OPTION", 0))
		})
		fixture.RoleGrantActor(run, "lo_new", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910213 TO lo_other", 0)) })
		fixture.RoleGrantActor(run, "lo_reader", func() {
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910214 TO lo_other", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910214 TO PUBLIC", 0))
		})
		// Keep an actual metadata handle across ACL rewriting and a second transfer.
		metadata := largeObjectOwnerHeld(t, p, b, c, largeObjectMetadataSQL)
		old := largeObjectOwnerOID(t, p, c, "lo_old")
		metadata(largeObjectMetadataStep(910212, [][]any{{int64(910212), old, "{}"}}))
		for _, oid := range []uint32{910211, 910212, 910213, 910214} { run(fixture.Exec(largeObjectOwnerAlter(oid, "lo_new"), 0)) }
		for i, acl := range []any{nil, "{}", "{lo_new=r*w/lo_new,lo_reader=w/lo_new,=r/lo_new,lo_other=r/lo_new}", "{lo_new=rw/lo_new,lo_reader=r*/lo_new,lo_other=r/lo_reader,=r/lo_reader}"} {
			oid := uint32(910211+i)
			largeObjectOwnerMetadata(run, oid, newOwner, acl)
			metadata(largeObjectMetadataStep(int64(oid), [][]any{{int64(oid), newOwner, acl}}))
			run(largeObjectValue("pg_get_acl(2995,"+fmtOID(oid)+"::oid,0)", "_ACLITEM", acl))
			run(largeObjectBytes(oid, "00017f80ff00"))
		}
		fixture.RoleGrantActor(run, "lo_new", func() {
			run(largeObjectError("SELECT lo_get(910212::oid)", "42501", "permission denied for large object 910212"))
			run(fixture.Exec(largeObjectOwnerAlter(910212, "CURRENT_USER"), 0))
			run(largeObjectValue("lo_unlink(910212::oid)", "INT4", int64(1)))
		})
		run(fixture.Exec("DROP ROLE lo_old", 0))
	})
}

func TestReferenceLargeObjectOwnerDependencies(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910221)
		largeObjectOwnerCreate(run, 910221)
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910221 TO lo_reader WITH GRANT OPTION", 0)) })
		fixture.RoleGrantActor(run, "lo_reader", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910221 TO lo_other", 0)) })
		run(largeObjectOwnerDropError("lo_old", "owner of large object 910221"))
		run(fixture.Exec(largeObjectOwnerAlter(910221, "lo_new"), 0))
		run(fixture.Exec("DROP ROLE lo_old", 0))
		run(largeObjectOwnerDropError("lo_new", "owner of large object 910221"))
		for _, role := range []string{"lo_reader", "lo_other"} { run(largeObjectOwnerDropError(role, "privileges for large object 910221")) }
		fixture.RoleGrantActor(run, "lo_reader", func() { run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 910221 FROM lo_other", 0)) })
		run(fixture.Exec("DROP ROLE lo_other", 0))
		run(largeObjectOwnerDropError("lo_reader", "privileges for large object 910221"))
		fixture.RoleGrantActor(run, "lo_new", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910221 FROM lo_reader", 0))
			run(largeObjectValue("lo_unlink(910221::oid)", "INT4", int64(1)))
		})
		run(fixture.Exec("DROP ROLE lo_reader", 0))
		run(fixture.Exec("DROP ROLE lo_new", 0))
	})
}
