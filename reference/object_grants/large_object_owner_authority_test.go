package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func fmtOID(oid uint32) string { return fmt.Sprint(oid) }

func TestReferenceLargeObjectOwnerAuthority(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910231)
		largeObjectOwnerCreate(run, 910231)
		old := largeObjectOwnerOID(t, p, c, "lo_old")
		q := largeObjectOwnerAlter(910231, "lo_new")
		denyOwner := func() { run(largeObjectOwnerError(q, "42501", "must be owner of large object 910231")) }
		denySet := func() { run(largeObjectOwnerError(q, "42501", `must be able to SET ROLE "lo_new"`)) }
		unchanged := func(acl any) {
			largeObjectOwnerMetadata(run, 910231, old, acl)
			run(largeObjectBytes(910231, "00017f80ff00"))
			run(largeObjectOwnerDropError("lo_old", "owner of large object 910231"))
		}
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec(largeObjectOwnerAlter(910231, "lo_old"), 0)) })
		fixture.RoleGrantActor(run, "lo_actor", func() {
			run(fixture.Exec(largeObjectOwnerAlter(910231, "lo_old"), 0))
			denyOwner()
			run(largeObjectOwnerError(largeObjectOwnerAlter(910231, "lo_missing"), "42704", `role "lo_missing" does not exist`))
			run(largeObjectOwnerError(largeObjectOwnerAlter(910239, "lo_missing"), "42704", `role "lo_missing" does not exist`))
			run(largeObjectOwnerError(largeObjectOwnerAlter(910239, "lo_new"), "42704", "large object 910239 does not exist"))
		})
		unchanged(nil)
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910231 TO lo_actor WITH GRANT OPTION", 0)) })
		fixture.RoleGrantActor(run, "lo_actor", denyOwner)
		unchanged("{lo_old=rw/lo_old,lo_actor=r*w*/lo_old}")
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910231 FROM lo_actor", 0)) })
		// SET-only membership in the owner does not provide inherited ownership.
		run(fixture.Exec("GRANT lo_old TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		run(fixture.Exec("GRANT lo_new TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", denyOwner)
		// Explicitly assuming the owner works once that principal can SET target.
		run(fixture.Exec("GRANT lo_new TO lo_old WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_old", func() { run(fixture.Exec(q, 0)) })
		run(fixture.Exec(largeObjectOwnerAlter(910231, "lo_old"), 0))
		run(fixture.Exec("REVOKE lo_new FROM lo_old", 0))
		// Independently change each privilege path without changing the SQL handle.
		held := largeObjectOwnerHeld(t, p, b, c, q)
		run(fixture.Exec("GRANT lo_old TO lo_actor WITH INHERIT TRUE, SET FALSE", 0))
		run(fixture.Exec("GRANT lo_new TO lo_actor WITH INHERIT TRUE, SET FALSE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() {
			denySet()
			held(largeObjectOwnerError(q, "42501", `must be able to SET ROLE "lo_new"`))
		})
		run(fixture.Exec("GRANT lo_new TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(fixture.Exec(q, 0)) })
		run(fixture.Exec(largeObjectOwnerAlter(910231, "lo_old"), 0))
		run(fixture.Exec("REVOKE lo_old,lo_new FROM lo_actor", 0))
		// Two independent multihop paths: all-INHERIT to owner, all-SET to target.
		run(fixture.Exec("GRANT lo_old TO lo_mid WITH INHERIT TRUE, SET FALSE", 0))
		run(fixture.Exec("GRANT lo_mid TO lo_actor WITH INHERIT TRUE, SET FALSE", 0))
		run(fixture.Exec("GRANT lo_new TO lo_alt WITH INHERIT FALSE, SET TRUE", 0))
		run(fixture.Exec("GRANT lo_alt TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(fixture.Exec(q, 0)) })
		run(fixture.Exec(largeObjectOwnerAlter(910231, "lo_old"), 0))
		run(fixture.Exec("GRANT lo_mid TO lo_actor WITH INHERIT FALSE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() {
			denyOwner()
			held(largeObjectOwnerError(q, "42501", "must be owner of large object 910231"))
		})
		run(fixture.Exec("GRANT lo_old TO lo_actor WITH INHERIT TRUE, SET FALSE", 0))
		run(fixture.Exec("GRANT lo_alt TO lo_actor WITH SET FALSE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() {
			denySet()
			held(largeObjectOwnerError(q, "42501", `must be able to SET ROLE "lo_new"`))
		})
		run(fixture.Exec("GRANT lo_new TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() {
			run(largeObjectValue("has_database_privilege(current_user,current_database(),'CREATE')", "BOOL", false))
			run(largeObjectValue("has_schema_privilege(current_user,'public','CREATE')", "BOOL", false))
			held(fixture.Exec(q, 0))
		})
		// Old owner lost actual authority, recipient gained it despite NOLOGIN.
		fixture.RoleGrantActor(run, "lo_old", func() { run(largeObjectError("SELECT lo_unlink(910231::oid)", "42501", "must be owner of large object 910231")) })
		fixture.RoleGrantActor(run, "lo_new", func() { run(largeObjectValue("lo_unlink(910231::oid)", "INT4", int64(1))) })
		largeObjectOwnerCreate(run, 910231)
		// Superuser needs neither membership path.
		run(fixture.Exec(q, 0))
		largeObjectOwnerMetadata(run, 910231, largeObjectOwnerOID(t, p, c, "lo_new"), nil)
	})
}

func TestReferenceLargeObjectOwnerPrepared(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910241, 910242)
		q := largeObjectOwnerAlter(910241, "lo_future")
		held := largeObjectOwnerHeld(t, p, b, c, q)
		run(largeObjectMetadataStep(910241, nil))
		held(largeObjectOwnerError(q, "42704", `role "lo_future" does not exist`))
		run(fixture.Exec("CREATE ROLE lo_future NOLOGIN", 0))
		held(largeObjectOwnerError(q, "42704", "large object 910241 does not exist"))
		largeObjectOwnerCreate(run, 910241)
		largeObjectOwnerActor(run, "lo_actor", func() { held(largeObjectOwnerError(q, "42501", "must be owner of large object 910241")) })
		run(fixture.Exec("GRANT lo_old TO lo_actor WITH INHERIT TRUE", 0))
		run(fixture.Exec("GRANT lo_future TO lo_actor WITH INHERIT FALSE, SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(fixture.Exec(q, 0)) })
		largeObjectOwnerMetadata(run, 910241, largeObjectOwnerOID(t, p, c, "lo_future"), nil)
		// A peer's committed transfer must invalidate no held authority snapshot.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(largeObjectOwnerAlter(910241, "lo_reader"), 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(largeObjectOwnerError(q, "42501", "must be owner of large object 910241")) })
		run(largeObjectValue("lo_unlink(910241::oid)", "INT4", int64(1)))
		largeObjectOwnerCreate(run, 910241)
		run(fixture.Exec("GRANT lo_future TO lo_actor WITH SET FALSE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(largeObjectOwnerError(q, "42501", `must be able to SET ROLE "lo_future"`)) })
		run(fixture.Exec("GRANT lo_future TO lo_actor WITH SET TRUE", 0))
		fixture.RoleGrantActor(run, "lo_actor", func() { held(fixture.Exec(q, 0)) })
		run(fixture.Exec(largeObjectOwnerAlter(910241, "lo_old"), 0))
		run(fixture.Exec("REVOKE lo_future FROM lo_actor", 0))
		run(fixture.Exec("DROP ROLE lo_future", 0))
		held(largeObjectOwnerError(q, "42704", `role "lo_future" does not exist`))
		run(fixture.Exec("CREATE ROLE lo_future NOLOGIN", 0))
		held(fixture.Exec(q, 0))
		largeObjectOwnerMetadata(run, 910241, largeObjectOwnerOID(t, p, c, "lo_future"), nil)
		// Selectors resolve the current principal on each execution, not Prepare.
		largeObjectOwnerCreate(run, 910242)
		for _, selector := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			query := largeObjectOwnerAlter(910242, selector)
			statement := largeObjectOwnerHeld(t, p, b, c, query)
			run(fixture.Exec(largeObjectOwnerAlter(910242, "lo_old"), 0))
			if selector == "SESSION_USER" {
				// Current actor must be allowed to SET the authenticated recipient.
				var session string
				if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&session); err != nil { t.Fatal(err) }
				run(fixture.Exec("GRANT \""+session+"\" TO lo_actor WITH SET TRUE", 0))
				largeObjectOwnerActor(run, "lo_actor", func() { statement(fixture.Exec(query, 0)) })
				largeObjectOwnerMetadata(run, 910242, largeObjectOwnerOID(t, p, c, session), nil)
				run(fixture.Exec("REVOKE \""+session+"\" FROM lo_actor", 0))
			} else {
				largeObjectOwnerActor(run, "lo_actor", func() { statement(fixture.Exec(query, 0)) })
				largeObjectOwnerMetadata(run, 910242, largeObjectOwnerOID(t, p, c, "lo_actor"), nil)
			}
		}
		for _, role := range []struct{ sql, name string }{{`"Lo Mixed"`, "Lo Mixed"}, {`"lo""quote"`, "lo\"quote"}} {
			run(fixture.Exec("CREATE ROLE "+role.sql, 0))
			query := largeObjectOwnerAlter(910242, role.sql)
			statement := largeObjectOwnerHeld(t, p, b, c, query)
			statement(fixture.Exec(query, 0))
			largeObjectOwnerMetadata(run, 910242, largeObjectOwnerOID(t, p, c, role.name), nil)
		}
	})
}
