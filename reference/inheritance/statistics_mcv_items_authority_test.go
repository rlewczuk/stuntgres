package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func mcvItemsSmokeAuthority(t *testing.T, s ndistinctScenario) {
	t.Helper()
	s.run(fixture.Exec("CREATE ROLE mcv_smoke_actor", 0))
	defer s.conn.ExecContext(s.pair.Ctx, "RESET ROLE")
	s.run(fixture.Exec("SET ROLE mcv_smoke_actor", 0))
	step := statisticsError("SELECT m.* FROM pg_statistic_ext_data d CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE FALSE", "42501", "permission denied for table pg_statistic_ext_data")
	step.SelectRows = true
	s.run(step)
	s.run(fixture.Exec("RESET ROLE", 0))
	s.run(fixture.Exec("DROP ROLE mcv_smoke_actor", 0))
}

func TestReferenceStatisticsMCVItemsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mcv_items_actor", "mcv_items_owner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, step) }
			s := ndistinctScenario{run: run, pg: b.PG, pair: p, conn: admin}
			mcvSetup(t, s)
			for _, q := range []string{"CREATE ROLE mcv_items_actor", "CREATE ROLE mcv_items_owner", "GRANT USAGE ON SCHEMA public TO mcv_items_actor,mcv_items_owner"} {
				run(fixture.Exec(q, 0))
			}
			defer func() {
				// Fixed catalog/function ACLs outlive the fixture's public
				// objects and otherwise prevent PostgreSQL role cleanup.
				for _, q := range []string{
					"GRANT EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) TO PUBLIC",
					"REVOKE EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) FROM mcv_items_actor",
					"REVOKE SELECT ON pg_statistic_ext_data FROM mcv_items_actor",
					"REVOKE USAGE ON SCHEMA public FROM mcv_items_actor,mcv_items_owner",
				} {
					run(fixture.Exec(q, 0))
				}
			}()
			actor, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer actor.Close()
			defer actor.ExecContext(p.Ctx, "RESET ROLE")
			arun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, actor, b.Recorder, step) }
			arun(fixture.Exec("SET ROLE mcv_items_actor", 0))
			for _, prepared := range []bool{false, true} {
				for _, suffix := range []string{"", " AND FALSE"} {
					q := "SELECT m.* FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='mcv_s'" + suffix
					step := statisticsError(q, "42501", "permission denied for table pg_statistic_ext_data")
					step.SelectRows = true
					step.Prepared = prepared
					arun(step)
				}
			}
			run(fixture.Exec("GRANT SELECT ON pg_statistic_ext_data TO mcv_items_actor", 0))
			held, err := actor.PrepareContext(p.Ctx, mcvItemsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			checkHeld := func() {
				t.Helper()
				rows, err := held.QueryContext(p.Ctx, "mcv_s")
				if err != nil {
					t.Fatal(err)
				}
				mcvItemsAssert(t, mcvItemsConsume(t, rows, b.PG), mcvItemsFour...)
			}
			checkHeld() // An explicit grant legitimately exposes another owner's facts.
			old, err := held.QueryContext(p.Ctx, "mcv_s")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if b.PG {
				mcvItemsAssert(t, mcvItemsConsume(t, old, true), mcvItemsFour...)
			}
			run(fixture.Exec("REVOKE SELECT ON pg_statistic_ext_data FROM mcv_items_actor", 0))
			rows, err := held.QueryContext(p.Ctx, "mcv_s")
			if rows != nil {
				rows.Close()
				t.Fatal("revoked held execution exposed rows")
			}
			fixture.AssertDiagnostic(t, err, diagnosticMCVData())
			if !b.PG {
				mcvItemsAssert(t, mcvItemsConsume(t, old, false), mcvItemsFour...)
			}
			run(fixture.Exec("GRANT SELECT ON pg_statistic_ext_data TO mcv_items_actor", 0))
			checkHeld()
			for _, prepared := range []bool{false, true} {
				target := s
				target.conn, target.prepared = actor, prepared
				mcvItemsCheck(t, target, "mcv_s", mcvItemsFour...)
			}
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) FROM PUBLIC", 0))
			rows, err = held.QueryContext(p.Ctx, "mcv_s")
			if rows != nil {
				rows.Close()
				t.Fatal("revoked routine exposed rows")
			}
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_mcv_list_items")
			fixture.AssertDiagnostic(t, err, &d)
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic("SELECT * FROM pg_mcv_list_items(NULL::pg_mcv_list)", d)
				step.SelectRows, step.Prepared = true, prepared
				arun(step)
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) TO mcv_items_actor", 0))
			checkHeld()
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) TO PUBLIC", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_mcv_list_items(pg_mcv_list) FROM mcv_items_actor", 0))
			run(fixture.Exec("ALTER TABLE mcv_t OWNER TO mcv_items_owner", 0))
			arun(fixture.Exec("RESET ROLE", 0))
			arun(fixture.Exec("SET ROLE mcv_items_owner", 0))
			step := statisticsError("SELECT m.* FROM pg_statistic_ext_data d CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE FALSE", "42501", "permission denied for table pg_statistic_ext_data")
			step.SelectRows = true
			arun(step)
			arun(fixture.Prepared(step))
			arun(fixture.Exec("RESET ROLE", 0))
		})
	}
}
