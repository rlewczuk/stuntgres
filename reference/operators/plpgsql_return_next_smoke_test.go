package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLReturnNextFeatureSmoke(t *testing.T) {
	t.Run("unchanged_original_repro", func(t *testing.T) { plpgsqlScenario(t, returnNextRepro) })
	t.Run("original577_consumer", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			returnNextMigrationSetup(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO external_auth_links(provider_id,user_id) VALUES('test','"+returnNextUser+"')", 1))
			stmt, err := c.PrepareContext(p.Ctx, returnNextLeaseQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: returnNextLeaseQuery}
			success := sqlExecutionStep(b, returnNextLeaseQuery, []string{"provider_id", "user_id", "has_expiry", "future"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"test", returnNextUser, true, true}}, "test", returnNextUser, int64(60000))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, returnNextLeaseQuery, []string{"provider_id", "user_id", "has_expiry", "future"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, nil, "missing", returnNextUser, int64(60000)))
			d := fixture.ControlsReferenceDiagnostic("23514", "row is currently leased by another replica")
			d.Constraint = "external_auth_link_active_lease"
			d.Context = "PL/pgSQL function acquire_external_auth_link_refresh_lease(text,uuid,bigint) line 19 at RAISE"
			s := fixture.ErrorDiagnostic(returnNextLeaseQuery, d, "test", returnNextUser, int64(60000))
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE external_auth_links SET refresh_lease_expires_at=NULL", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
		})
	})
}
