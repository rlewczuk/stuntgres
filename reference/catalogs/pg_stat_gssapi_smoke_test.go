package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatGSSAPIFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gss_smoke_login", "gss_smoke_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			gssapiCreateRoles(t, p, b, "gss_smoke_login", "gss_smoke_actor")
			gssapiSetupSentinel(t, p, b)
			admin := gssapiConn(t, p, b.DB)
			_, a := gssapiLogin(t, p, b, "gss_smoke_login", 0)
			peerPool, peer := gssapiLogin(t, p, b, "gss_smoke_login", 0)
			_, actor := gssapiLogin(t, p, b, "gss_smoke_actor", 0)
			aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
			if aPID == peerPID {
				t.Fatal("physical sessions share PID", aPID)
			}
			ordered := gssapiTuples(aPID, peerPID)
			if aPID > peerPID {
				ordered = gssapiTuples(peerPID, aPID)
			}
			query := gssapiQuery + " WHERE pid IN ($1,$2) ORDER BY pid"
			step := fixture.Select(query, gssapiNames, gssapiTypes, ordered, aPID, peerPID)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { gssapiMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(step))
			statement, err := actor.PrepareContext(p.Ctx, gssapiPIDQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: statement, SQL: gssapiPIDQuery}
			gssapiBoth(t, p, b, actor, gssapiPIDQuery, gssapiTuples(), peerPID)
			gssapiRead(t, p, b, held, false, gssapiPIDQuery, gssapiTuples(), peerPID)
			gssapiExec(t, p, admin, "GRANT gss_smoke_login TO gss_smoke_actor")
			gssapiRead(t, p, b, held, false, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
			gssapiBoth(t, p, b, actor, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
			for _, prepared := range []bool{false, true} {
				failure := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_gssapi SET encrypted=true", gssapiWriteDiagnostic("UPDATE"))
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				if prepared {
					failure.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, admin, b.Recorder, failure)
				gssapiSentinel(t, p, b, admin)
				gssapiBoth(t, p, b, admin, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			}
			gssapiRetire(t, p, b, admin, peerPool, peer, peerPID)
			gssapiClear(t, p, actor)
			gssapiRead(t, p, b, held, false, gssapiPIDQuery, gssapiTuples(), peerPID)
			gssapiBoth(t, p, b, actor, query, gssapiTuples(aPID), aPID, peerPID)
			gssapiBoth(t, p, b, admin, query, gssapiTuples(aPID), aPID, peerPID)
			gssapiSentinel(t, p, b, admin)
		})
	}
}
