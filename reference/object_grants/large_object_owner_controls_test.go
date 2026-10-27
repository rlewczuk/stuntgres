package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectOwnerControls(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910281, 910282, 910283, 910284)
		for _, oid := range []uint32{910281, 910282, 910283} { largeObjectOwnerCreate(run, oid) }
		fixture.RoleGrantActor(run, "lo_old", func() {
			run(largeObjectPut(910281, 4294967296, "00ff00"))
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910282 FROM lo_old", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910283 TO lo_new WITH GRANT OPTION", 0))
			run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910283 TO lo_reader", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910283 TO PUBLIC", 0))
		})
		fixture.RoleGrantActor(run, "lo_new", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910283 TO lo_other", 0)) })
		for _, oid := range []uint32{910281, 910282, 910283} { run(fixture.Exec(largeObjectOwnerAlter(oid, "lo_new"), 0)) }
		owner := largeObjectOwnerOID(t, p, c, "lo_new")
		verify := func() {
			for i, acl := range []any{nil, "{}", "{lo_new=r*w/lo_new,lo_reader=w/lo_new,=r/lo_new,lo_other=r/lo_new}"} {
				largeObjectOwnerMetadata(run, uint32(910281+i), owner, acl)
			}
			run(largeObjectValue("encode(lo_get(910281::oid,0::bigint,6),'hex')", "TEXT", "00017f80ff00"))
			run(largeObjectValue("encode(lo_get(910281::oid,4294967294::bigint,8),'hex')", "TEXT", "000000ff00"))
			run(largeObjectBytes(910282, "00017f80ff00"))
			run(largeObjectBytes(910283, "00017f80ff00"))
			run(fixture.Select("SELECT pageno,data FROM pg_largeobject WHERE loid=910281::oid ORDER BY pageno", []string{"pageno", "data"}, []string{"INT4", "BYTEA"}, [][]any{
				{int64(0), string([]byte{0, 1, 0x7f, 0x80, 0xff, 0})}, {int64(2097152), string([]byte{0, 0xff, 0})},
			}))
			run(largeObjectOwnerDropError("lo_reader", "privileges for large object 910283"))
			run(largeObjectOwnerDropError("lo_other", "privileges for large object 910283"))
			fixture.RoleGrantActor(run, "lo_new", func() { run(largeObjectError("SELECT lo_get(910282::oid)", "42501", "permission denied for large object 910282")) })
		}
		verify()
		if !b.PG {
			// Private reverse ownership is deliberately excluded from Dump.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(largeObjectOwnerAlter(910281, "lo_old"), 0))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			if dump.Len() == 0 { t.Fatal("empty Dump") }
			run(fixture.Exec("ROLLBACK", 0))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			for _, oid := range []uint32{910281, 910282, 910283} { run(largeObjectMetadataStep(int64(oid), nil)) }
			if largeObjectOwnerOID(t, p, c, "lo_new") != owner { t.Fatal("Reset replaced role") }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			verify()
			run(fixture.Exec("DROP ROLE lo_old", 0))
			// Exact generated stream must no longer depend on the obsolete owner.
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(redump.Bytes())); err != nil { t.Fatal(err) }
			verify()
			var repeated bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &repeated); err != nil { t.Fatal(err) }
			if !bytes.Equal(redump.Bytes(), repeated.Bytes()) { t.Fatal("semantic restore/redump changed canonical stream") }
			run(largeObjectValue("lo_from_bytea(910284::oid,decode('5a00','hex'))", "OID", int64(910284)))
			prefix := redump.String()+"\nSELECT pg_catalog.lo_put(910281::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('aabb','hex'));\n"
			for _, invalid := range []string{
				"SELECT pg_catalog.lo_get(910281::pg_catalog.oid);",
				"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{lo_missing=r/lo_new}'::pg_catalog.aclitem[] WHERE oid=910283::pg_catalog.oid;",
			} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+invalid)); err == nil { t.Fatal("invalid Restore succeeded", invalid) }
				verify()
				run(largeObjectBytes(910284, "5a00"))
			}
		} else {
			run(fixture.Exec("DROP ROLE lo_old", 0))
		}
		// Isolate owner from the two ACL dependencies so exact dependency detail
		// is stable without sorting or assuming independent-object order.
		fixture.RoleGrantActor(run, "lo_new", func() {
			run(largeObjectValue("lo_unlink(910281::oid)", "INT4", int64(1)))
			run(largeObjectValue("lo_unlink(910282::oid)", "INT4", int64(1)))
		})
		run(largeObjectOwnerDropError("lo_new", "owner of large object 910283"))
	})
}
