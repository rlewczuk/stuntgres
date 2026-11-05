package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func restoreconOrdinaryIdentity(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	restoreconInactive(t, p, backend, c, run)
	run(fixture.Exec("CREATE FUNCTION restorecon_ordinary(text) RETURNS boolean LANGUAGE SQL AS 'SELECT true'", 0))
	var original, renamed int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'restorecon_ordinary(text)'::regprocedure::oid::bigint").Scan(&original); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("ALTER FUNCTION restorecon_ordinary(text) RENAME TO restorecon_renamed", 0))
	if err := c.QueryRowContext(p.Ctx, "SELECT 'restorecon_renamed(text)'::regprocedure::oid::bigint").Scan(&renamed); err != nil || original != renamed {
		t.Fatal("ordinary rename changed genuine identity", original, renamed, err)
	}
	for _, prepared := range []bool{false, true} {
		restoreconCall(run, "restorecon_renamed", nil, prepared)
	}
	run(fixture.Exec("CREATE OR REPLACE FUNCTION restorecon_renamed(text) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
	for _, prepared := range []bool{false, true} {
		step := sepgsqlBool("SELECT restorecon_renamed(NULL) AS replaced", "replaced", false)
		step.Prepared = prepared
		run(step)
	}
	run(fixture.Exec("DROP FUNCTION restorecon_renamed(text)", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('restorecon_ordinary','restorecon_renamed')", 0))
	restoreconDroppedFacts(t, p, c, run, false)
}

func restoreconRoundTrip(t *testing.T, p *fixture.Pair, image string) {
	t.Helper()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(image)); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceSepgsqlSimulationRestoreconIdentity(t *testing.T) {
	sepgsqlCase(t, restoreconOrdinaryIdentity, restoreconDroppedConfig, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconDroppedSetup(run)
		var original, renamed int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'sepgsql_restorecon(text)'::regprocedure::oid::bigint").Scan(&original); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER FUNCTION sepgsql_restorecon(text) RENAME TO restored_restorecon", 0))
		if err := c.QueryRowContext(p.Ctx, "SELECT 'restored_restorecon(text)'::regprocedure::oid::bigint").Scan(&renamed); err != nil || renamed != original {
			t.Fatal("authentic rename changed declaration identity", original, renamed, err)
		}
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_dropped IS '"+sepgsqlS+"'", 0))
			restoreconCall(run, "restored_restorecon", "tombstone", prepared)
			restoreconDroppedLabels(run, sepgsqlS)
		}
		authentic := fixture.ExtensionDump(t, p.Ctx, p.Native)
		t.Run("fresh_destination", func(t *testing.T) {
			target := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				config := sepgsqlConfig()
				restoreconDroppedConfig(&config)
				return d.RegisterSepgsqlSimulation(config)
			})
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(authentic)); err != nil {
				t.Fatal(err)
			}
			for _, b := range target.Backends() {
				if b.PG {
					continue
				}
				runTarget := func(step fixture.Step) { fixture.RunStep(t, target.Ctx, b.DB, b.Recorder, step) }
				runTarget(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0))
				for _, prepared := range []bool{false, true} {
					runTarget(fixture.Exec("SECURITY LABEL ON TABLE restorecon_dropped IS '"+sepgsqlS+"'", 0))
					restoreconCall(runTarget, "restored_restorecon", "tombstone", prepared)
					restoreconDroppedLabels(runTarget, sepgsqlS)
				}
				restoreconDroppedFacts(t, target, b.DB, runTarget, true)
			}
		})
		restoreconRoundTrip(t, p, authentic)
		for _, prepared := range []bool{false, true} {
			restoreconCall(run, "restored_restorecon", "tombstone", prepared)
			restoreconDroppedLabels(run, sepgsqlS)
		}
		// Same spelling is never a substitute for the sealed installed body.
		run(fixture.Exec("CREATE FUNCTION pg_catalog.sepgsql_restorecon(text) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
		before := sepgsqlRestoreconLabels(t, p, c)
		for _, prepared := range []bool{false, true} {
			step := sepgsqlBool("SELECT sepgsql_restorecon(NULL) AS impostor", "impostor", false)
			step.Prepared = prepared
			run(step)
			restoreconUnchanged(t, p, c, before)
		}
		run(fixture.Exec("CREATE OR REPLACE FUNCTION pg_catalog.restored_restorecon(text) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
		before = sepgsqlRestoreconLabels(t, p, c)
		assertReplacement := func() {
			for _, prepared := range []bool{false, true} {
				step := sepgsqlBool("SELECT restored_restorecon(NULL) AS replaced", "replaced", false)
				step.Prepared = prepared
				run(step)
				restoreconUnchanged(t, p, c, before)
			}
		}
		assertReplacement()
		replacement := fixture.ExtensionDump(t, p.Ctx, p.Native)
		restoreconRoundTrip(t, p, replacement)
		// Destination object OIDs are reminted: capture its real before-state.
		before = sepgsqlRestoreconLabels(t, p, c)
		assertReplacement()
		restoreconDroppedLabels(run, sepgsqlS)
		// Restore the authentic image, then actually retire that capability.
		restoreconRoundTrip(t, p, authentic)
		run(fixture.Exec("DROP FUNCTION restored_restorecon(text)", 0))
		retired := fixture.ExtensionDump(t, p.Ctx, p.Native)
		restoreconRoundTrip(t, p, retired)
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('sepgsql_restorecon','restored_restorecon')", 0))
		restoreconDroppedLabels(run, sepgsqlS)
		restoreconDroppedFacts(t, p, c, run, true)
	})
}

func TestReferenceSepgsqlSimulationRestoreconFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "restorecon_phase")
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, restoreconOrdinaryIdentity, func(config *stuntgres.SepgsqlSimulationConfig) {
		restoreconDroppedConfig(config)
		restoreconTransactionConfig(config)
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { events = append(events, event) }
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconPhaseSetup(run)
		for _, prepared := range []bool{false, true} {
			events = nil
			restoreconCall(run, "sepgsql_restorecon", "tombstone", prepared)
			restoreconAssertPhases(t, events)
			restoreconDroppedLabels(run, sepgsqlS)
			before := sepgsqlRestoreconLabels(t, p, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT labels", 0))
			restoreconCall(run, "sepgsql_restorecon", nil, prepared)
			restoreconDroppedLabels(run, sepgsqlP)
			run(fixture.Exec("ROLLBACK TO labels", 0))
			restoreconUnchanged(t, p, c, before)
			step := sepgsqlDenied("SELECT sepgsql_restorecon('denied')")
			step.Prepared = prepared
			run(step)
			run(fixture.Exec("ROLLBACK TO labels", 0))
			restoreconUnchanged(t, p, c, before)
			run(fixture.Exec("COMMIT", 0))
			restoreconUnchanged(t, p, c, before)
		}
		run(fixture.Exec("ALTER FUNCTION sepgsql_restorecon(text) RENAME TO smoke_restorecon", 0))
		image := fixture.ExtensionDump(t, p.Ctx, p.Native)
		restoreconRoundTrip(t, p, image)
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0))
		restoreconDroppedFacts(t, p, c, run, true)
		restoreconDroppedLabels(run, sepgsqlS)
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_dropped IS '"+sepgsqlS+"'", 0))
			events = nil
			restoreconCall(run, "smoke_restorecon", "tombstone", prepared)
			restoreconAssertPhases(t, events)
			restoreconDroppedLabels(run, sepgsqlS)
		}
	})
}
