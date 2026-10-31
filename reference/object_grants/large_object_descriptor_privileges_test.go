package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorPrivileges(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910188)
		adminConn, admin := descriptorPeer(t, p, b, prepared)
		defer func() {
			// A failed assertion can leave the actor's transaction aborted. End it
			// before restoring shared routine privileges on the independent admin.
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, conn := range []*sql.Conn{c, adminConn} {
				if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
					t.Error("descriptor privilege cleanup rollback", err)
				}
			}
			if _, err := adminConn.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION pg_catalog.loread(integer,integer) TO PUBLIC"); err != nil {
				t.Error("descriptor privilege cleanup routine ACL", err)
			}
		}()
		run(largeObjectValue("lo_from_bytea(910188::oid,decode('616263646566','hex'))", "OID", int64(910188)))
		deny := func(mode int) fixture.Step {
			return largeObjectError("SELECT lo_open(910188::oid,$1::integer)", "42501", "permission denied for large object 910188", mode)
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(deny(descriptorRead))
			run(deny(descriptorWrite))
		})
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910188 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(deny(descriptorRead))
			run(deny(descriptorWrite))
		})
		run(fixture.Exec("REVOKE UPDATE ON LARGE OBJECT 910188 FROM og_object_reader", 0))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910188 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(deny(descriptorWrite))
			run(fixture.Exec("BEGIN", 0))
			r := descriptorOpen(t, p, c, prepared, 910188, descriptorRead, 0)
			run(descriptorReadStep(r, 2, "ab"))
			// Failed unlink does not invalidate an already-open authorized reader.
			descriptorRecover(run, largeObjectError("SELECT lo_unlink(910188::oid)", "42501", "must be owner of large object 910188"))
			run(descriptorReadStep(r, 2, "cd"))
			run(fixture.Exec("ROLLBACK", 0))
		})
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910188 TO og_object_reader", 0))
		q := "SELECT loread($1::integer,$2::integer) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer held.Close()
		execute := func(s fixture.Step) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor:c, Statement:held, SQL:q}, b.Recorder, s)
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("BEGIN", 0))
			r := descriptorOpen(t, p, c, prepared, 910188, descriptorRead, 0)
			w := descriptorOpen(t, p, c, prepared, 910188, descriptorWrite, 1)
			run(descriptorReadStep(w, 1, "a"))
			admin(fixture.Exec("REVOKE SELECT,UPDATE ON LARGE OBJECT 910188 FROM og_object_reader", 0))
			run(descriptorReadStep(r, 2, "ab"))
			run(descriptorWriteStep(w, []byte("XY")))
			run(descriptorReadStep(w, 3, "def"))
			descriptorRecover(run, deny(descriptorRead))
			descriptorRecover(run, deny(descriptorWrite))
			// A held recipe uses the executing transaction's admitted descriptor,
			// preserving its cursor after object ACL revocation. Routine EXECUTE
			// plan caching is separate from descriptor authority.
			execute(descriptorReadStep(r, 1, "c"))
			execute(descriptorReadStep(r, 3, "def"))
			run(fixture.Exec("ROLLBACK", 0))
		})
		// Inherited object admission is borrowed from the normal role graph.
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910188 TO og_object_other", 0))
		run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("BEGIN", 0))
			r := descriptorOpen(t, p, c, prepared, 910188, descriptorRead, 0)
			run(descriptorReadStep(r, 6, "abcdef"))
			run(fixture.Exec("COMMIT", 0))
		})
	})
}
