package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectRestore(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910071, 910072, 910073, 910074, 910075)
		var ownerOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_other'").Scan(&ownerOID); err != nil {
			t.Fatal(err)
		}
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_from_bytea(910071::oid,decode('00017f80ff00','hex'))", "OID", int64(910071)))
			run(largeObjectValue("lo_create(910072::oid)", "OID", int64(910072)))
			run(largeObjectValue("lo_from_bytea(910073::oid,decode('616200','hex'))", "OID", int64(910073)))
			run(largeObjectPut(910073, 4294967296, "00ff00"))
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910072 FROM og_object_other", 0))
			run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910073 TO og_object_reader WITH GRANT OPTION", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910073 TO PUBLIC", 0))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910073 FROM og_object_other", 0))
		})
		// Explicitly ordered raw ACL expectations, not sorted/set-only parity.
		verify := func() {
			run(fixture.Select("SELECT oid,lomowner,lomacl,pg_get_acl(2995,oid,0) AS acl FROM pg_largeobject_metadata WHERE oid IN (910071,910072,910073) ORDER BY oid", []string{"oid", "lomowner", "lomacl", "acl"}, []string{"OID", "OID", "_ACLITEM", "_ACLITEM"}, [][]any{
				{int64(910071), ownerOID, nil, nil},
				{int64(910072), ownerOID, "{}", "{}"},
				{int64(910073), ownerOID, "{og_object_reader=r*w*/og_object_other,=r/og_object_reader}", "{og_object_reader=r*w*/og_object_other,=r/og_object_reader}"},
			}))
			run(largeObjectBytes(910071, "00017f80ff00"))
			run(largeObjectBytes(910072, ""))
			run(largeObjectValue("encode(lo_get(910073::oid,0::bigint,5),'hex')", "TEXT", "6162000000"))
			run(largeObjectValue("encode(lo_get(910073::oid,4294967294::bigint,8),'hex')", "TEXT", "000000ff00"))
			fixture.RoleGrantActor(run, "og_object_other", func() {
				run(largeObjectError("SELECT lo_get(910072::oid)", "42501", "permission denied for large object 910072"))
				run(largeObjectError("SELECT lo_put(910073::oid,0::bigint,decode('','hex'))", "42501", "permission denied for large object 910073"))
			})
		}
		verify()
		if !b.PG {
			run(fixture.Error("UPDATE pg_catalog.pg_largeobject_metadata SET lomacl=NULL::aclitem[] WHERE oid=910073::oid", "0A000"))
			verify()
			// Committed capture excludes writer-private bytes and identities.
			run(fixture.Exec("BEGIN", 0))
			run(largeObjectPut(910071, 0, "ffff"))
			run(largeObjectValue("lo_create(910074::oid)", "OID", int64(910074)))
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ROLLBACK", 0))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT oid,lomowner,lomacl FROM pg_catalog.pg_largeobject_metadata WHERE oid IN (910071,910072,910073,910074) ORDER BY oid", []string{"oid", "lomowner", "lomacl"}, []string{"OID", "OID", "_ACLITEM"}, nil))
			run(largeObjectMissing("SELECT lo_get(910071::oid)", 910071))
			// Restrictive destination defaults must not add/remove imported raw
			// ACLs or stop trusted byte replay under the original owner directive.
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other GRANT SELECT ON LARGE OBJECTS TO og_object_reader", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other REVOKE ALL ON LARGE OBJECTS FROM og_object_other", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			verify()
			run(fixture.Select("SELECT oid,lomowner,lomacl FROM pg_catalog.pg_largeobject_metadata WHERE oid=910074::oid", []string{"oid", "lomowner", "lomacl"}, []string{"OID", "OID", "_ACLITEM"}, nil))
			run(largeObjectMissing("SELECT lo_get(910074::oid)", 910074))
			run(largeObjectValue("lo_from_bytea(910075::oid,decode('5a00','hex'))", "OID", int64(910075)))
			// Valid prefix replay mutates only the private stage before each
			// late invalid SELECT, principal or widened raw ACL repair.
			prefix := snapshot.String() + "\nSELECT pg_catalog.lo_put(910071::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('aabb','hex'));\n"
			for _, invalid := range []string{
				"SELECT pg_catalog.lo_get(910071::pg_catalog.oid);",
				"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{og_lo_missing_role=r/og_object_other}'::pg_catalog.aclitem[] WHERE oid=910073::pg_catalog.oid;",
				"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[] WHERE oid=910073::pg_catalog.oid OR oid=910071::pg_catalog.oid;",
				"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[],lomowner=10 WHERE oid=910073::pg_catalog.oid;",
			} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+invalid)); err == nil {
					t.Fatal("late invalid Restore succeeded", invalid)
				}
				verify()
				run(largeObjectBytes(910075, "5a00"))
			}
		}
	})
}

func TestReferenceLargeObjectOIDCollision(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE og_lo_persistent_collision(v integer)", 0))
		run(fixture.Exec("INSERT INTO og_lo_persistent_collision VALUES(17)", 1))
		run(fixture.Exec("CREATE TEMP TABLE og_lo_temporary_collision(v integer)", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS og_lo_temporary_collision"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("INSERT INTO og_lo_temporary_collision VALUES(23)", 1))
		var persistent, temporary uint32
		if err := c.QueryRowContext(p.Ctx, "SELECT 'og_lo_persistent_collision'::regclass::oid,'og_lo_temporary_collision'::regclass::oid").Scan(&persistent, &temporary); err != nil {
			t.Fatal(err)
		}
		defer largeObjectCleanup(t, p, c, persistent, temporary)
		for _, oid := range []uint32{persistent, temporary} {
			run(largeObjectValue(fmt.Sprintf("lo_from_bytea(%d::oid,decode('00ff00','hex'))", oid), "OID", int64(oid)))
			run(largeObjectBytes(oid, "00ff00"))
		}
		verify := func() {
			run(fixture.Select("SELECT v FROM og_lo_persistent_collision", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}}))
			run(fixture.Select("SELECT v FROM og_lo_temporary_collision", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
			run(largeObjectValue("'og_lo_temporary_collision'::regclass::oid", "OID", int64(temporary)))
			run(largeObjectBytes(persistent, "00ff00"))
			run(largeObjectBytes(temporary, "00ff00"))
		}
		verify()
		if !b.PG {
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(largeObjectValue("'og_lo_temporary_collision'::regclass::oid", "OID", int64(temporary)))
			run(fixture.Select("SELECT v FROM og_lo_temporary_collision", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			verify()
		}
	})
}
