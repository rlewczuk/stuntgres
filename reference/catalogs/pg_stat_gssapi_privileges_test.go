package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatGSSAPIPrivileges(t *testing.T) {
	roles := []string{"gss_target", "gss_other", "gss_actor", "gss_bridge"}
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			gssapiCreateRoles(t, p, b, roles...)
			admin := gssapiConn(t, p, b.DB)
			peerPool, peer := gssapiLogin(t, p, b, "gss_target", 0)
			_, sameLogin := gssapiLogin(t, p, b, "gss_target", 0)
			_, actor := gssapiLogin(t, p, b, "gss_actor", 0)
			pid := gssapiPID(t, p, peer)
			gssapiBoth(t, p, b, peer, gssapiPIDQuery, gssapiTuples(pid), pid)
			gssapiBoth(t, p, b, sameLogin, gssapiPIDQuery, gssapiTuples(pid), pid)
			gssapiBoth(t, p, b, admin, gssapiPIDQuery, gssapiTuples(pid), pid)
			statement, err := actor.PrepareContext(p.Ctx, gssapiPIDQuery)
			if err != nil {
				t.Fatal("PUBLIC SELECT must permit ordinary prepare", err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: statement, SQL: gssapiPIDQuery}
			check := func(visible bool) {
				want := gssapiTuples()
				if visible {
					want = gssapiTuples(pid)
				}
				gssapiBoth(t, p, b, actor, gssapiPIDQuery, want, pid)
				gssapiRead(t, p, b, held, false, gssapiPIDQuery, want, pid)
			}
			gssapiExec(t, p, actor, "BEGIN")
			defer actor.ExecContext(context.Background(), "ROLLBACK")
			// The denied execution captures raw membership. Every subsequent rights
			// change must be observed without clearing that transaction's snapshot.
			check(false)
			gssapiExec(t, p, admin, "GRANT gss_target TO gss_actor")
			check(true)
			gssapiExec(t, p, admin, "REVOKE gss_target FROM gss_actor")
			check(false)
			gssapiExec(t, p, admin, "GRANT gss_target TO gss_bridge")
			gssapiExec(t, p, admin, "GRANT gss_bridge TO gss_actor")
			check(true)
			gssapiExec(t, p, admin, "REVOKE gss_bridge FROM gss_actor")
			check(false)
			for _, options := range []string{
				"INHERIT FALSE, SET TRUE, ADMIN FALSE",
				"INHERIT FALSE, SET FALSE, ADMIN TRUE",
				"INHERIT FALSE, SET TRUE, ADMIN TRUE",
			} {
				gssapiExec(t, p, admin, "GRANT gss_target TO gss_actor WITH "+options)
				check(false)
				if options != "INHERIT FALSE, SET FALSE, ADMIN TRUE" {
					gssapiExec(t, p, actor, "SET ROLE gss_target")
					check(true)
					gssapiExec(t, p, actor, "RESET ROLE")
					check(false)
				}
				gssapiExec(t, p, admin, "REVOKE gss_target FROM gss_actor")
			}
			for _, role := range []string{"pg_read_all_stats", "pg_monitor"} {
				gssapiExec(t, p, admin, "GRANT "+role+" TO gss_actor")
				check(true)
				gssapiExec(t, p, admin, "REVOKE "+role+" FROM gss_actor")
				check(false)
			}
			// A target's current role does not replace its immutable login identity.
			gssapiExec(t, p, admin, "GRANT gss_other TO gss_target WITH INHERIT FALSE, SET TRUE")
			gssapiExec(t, p, peer, "SET ROLE gss_other")
			defer peer.ExecContext(context.Background(), "RESET ROLE")
			gssapiExec(t, p, admin, "GRANT gss_target TO gss_actor")
			check(true)
			gssapiExec(t, p, admin, "REVOKE gss_target FROM gss_actor")
			gssapiExec(t, p, admin, "GRANT gss_other TO gss_actor")
			check(false)
			gssapiExec(t, p, admin, "REVOKE gss_other FROM gss_actor")
			gssapiExec(t, p, peer, "RESET ROLE")
			gssapiExec(t, p, actor, "COMMIT")

			// Relation SELECT and per-row visibility are independent authorities.
			defer admin.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_stat_gssapi TO PUBLIC")
			gssapiExec(t, p, admin, "REVOKE SELECT ON pg_catalog.pg_stat_gssapi FROM PUBLIC")
			for _, x := range []fixture.Executor{actor, held} {
				s := fixture.ImplicitError(gssapiPIDQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_stat_gssapi"}, pid)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			}
			gssapiExec(t, p, admin, "GRANT SELECT ON pg_catalog.pg_stat_gssapi TO gss_actor")
			check(false)
			gssapiExec(t, p, admin, "GRANT SELECT ON pg_catalog.pg_stat_gssapi TO PUBLIC")
			gssapiExec(t, p, admin, "REVOKE SELECT ON pg_catalog.pg_stat_gssapi FROM gss_actor")
			gssapiExec(t, p, admin, "GRANT gss_target TO gss_actor")
			retained, err := statement.QueryContext(p.Ctx, pid)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			gssapiMetadata(t, retained, b.PG)
			gssapiExec(t, p, admin, "REVOKE gss_target FROM gss_actor")
			gssapiRetire(t, p, b, admin, peerPool, peer, pid)
			gssapiConsume(t, retained, b.PG, gssapiTuples(pid))
			check(false)
		})
	}
}
