package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectPrivileges(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910041, 910042, 910043, 910044, 910045)
		var owner string
		var ownerOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user,oid FROM pg_roles WHERE rolname=current_user").Scan(&owner, &ownerOID); err != nil {
			t.Fatal(err)
		}
		run(largeObjectValue("lo_from_bytea(910041::oid,decode('6162','hex'))", "OID", int64(910041)))
		metadata := func(oid uint32, owner int64, acl any) {
			run(fixture.Select(fmt.Sprintf("SELECT oid,lomowner,lomacl,pg_get_acl(2995,oid,0) AS acl FROM pg_largeobject_metadata WHERE oid=%d::oid", oid), []string{"oid", "lomowner", "lomacl", "acl"}, []string{"OID", "OID", "_ACLITEM", "_ACLITEM"}, [][]any{{int64(oid), owner, acl, acl}}))
		}
		metadata(910041, ownerOID, nil)
		read := "SELECT lo_get(910041::oid)"
		write := "SELECT lo_put(910041::oid,0::bigint,decode('6364','hex'))"
		denied := func(q string) { run(largeObjectError(q, "42501", "permission denied for large object 910041")) }
		fixture.RoleGrantActor(run, "og_object_reader", func() { denied(read); denied(write) })
		// Ordinary relation ACLs and predefined all-table roles do not confer
		// object-byte authority (REL_18_6 inv_open).
		run(fixture.Exec("CREATE TABLE og_lo_table_authority(v integer)", 0))
		run(fixture.Exec("INSERT INTO og_lo_table_authority VALUES(17)", 1))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("GRANT SELECT,UPDATE ON og_lo_table_authority TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT v FROM og_lo_table_authority", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}}))
			run(fixture.Exec("UPDATE og_lo_table_authority SET v=23", 1))
			denied(read)
			denied(write)
		})
		run(fixture.Exec("GRANT pg_read_all_data,pg_write_all_data TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { denied(read); denied(write) })
		run(fixture.Exec("REVOKE pg_read_all_data,pg_write_all_data FROM og_object_reader", 0))
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910041 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { denied(read); denied(write) })
		run(fixture.Exec("REVOKE UPDATE ON LARGE OBJECT 910041 FROM og_object_reader", 0))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910041 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(largeObjectBytes(910041, "6162")); denied(write) })
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910041 TO og_object_reader WITH GRANT OPTION", 0))
		metadata(910041, ownerOID, "{"+owner+"=rw/"+owner+",og_object_reader=rw*/"+owner+"}")
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectPut(910041, 0, "6364"))
			run(largeObjectError("SELECT lo_unlink(910041::oid)", "42501", "must be owner of large object 910041"))
			run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910041 TO og_object_other", 0))
		})
		metadata(910041, ownerOID, "{"+owner+"=rw/"+owner+",og_object_reader=rw*/"+owner+",og_object_other=w/og_object_reader}")
		run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910041 FROM og_object_reader CASCADE", 0))
		run(largeObjectBytes(910041, "6364"))
		// Real non-superuser owner, with inherited ownership kept independent of
		// ordinary SELECT/UPDATE. No ALTER LARGE OBJECT OWNER is needed.
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_from_bytea(910042::oid,decode('00ff','hex'))", "OID", int64(910042)))
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910042 FROM og_object_other", 0))
			run(largeObjectError("SELECT lo_get(910042::oid)", "42501", "permission denied for large object 910042"))
			run(largeObjectError("SELECT lo_put(910042::oid,0::bigint,decode('','hex'))", "42501", "permission denied for large object 910042"))
		})
		var otherOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_other'").Scan(&otherOID); err != nil {
			t.Fatal(err)
		}
		metadata(910042, otherOID, "{}")
		run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectError("SELECT lo_get(910042::oid)", "42501", "permission denied for large object 910042"))
			run(largeObjectValue("lo_unlink(910042::oid)", "INT4", int64(1)))
		})
		run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
		// Global creation defaults, including failure of from_bytea's write-open.
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other REVOKE SELECT ON LARGE OBJECTS FROM og_object_reader", "ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other GRANT ALL ON LARGE OBJECTS TO og_object_other"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other GRANT SELECT ON LARGE OBJECTS TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_create(910043::oid)", "OID", int64(910043)))
			run(largeObjectValue("lo_from_bytea(910044::oid,decode('7a','hex'))", "OID", int64(910044)))
		})
		metadata(910043, otherOID, "{og_object_reader=r/og_object_other,og_object_other=rw/og_object_other}")
		metadata(910044, otherOID, "{og_object_reader=r/og_object_other,og_object_other=rw/og_object_other}")
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(largeObjectBytes(910044, "7a")) })
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910043,910044 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectPut(910043, 9000, ""))
			run(largeObjectPut(910044, 9000, ""))
		})
		run(fixture.Exec("REVOKE UPDATE ON LARGE OBJECT 910043,910044 FROM og_object_reader", 0))
		// A real earlier target is not granted on failure of a later target.
		fixture.RoleGrantError(run, "GRANT UPDATE ON LARGE OBJECT 910043,910045 TO og_object_reader", "42704", "large object 910045 does not exist")
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectError("SELECT lo_put(910043::oid,0::bigint,decode('','hex'))", "42501", "permission denied for large object 910043"))
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE og_object_other REVOKE ALL ON LARGE OBJECTS FROM og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectError("SELECT lo_from_bytea(910045::oid,decode('7a','hex'))", "42501", "permission denied for large object 910045"))
		})
		run(largeObjectMissing("SELECT lo_get(910045::oid)", 910045))
		run(largeObjectValue("lo_create(910045::oid)", "OID", int64(910045)))
	})
}
