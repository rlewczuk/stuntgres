package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func mcvSmokeAuthority(t *testing.T, s ndistinctScenario) {
	t.Helper()
	s.run(fixture.Exec("CREATE ROLE mcv_smoke_actor", 0))
	defer func() {
		s.conn.ExecContext(s.pair.Ctx, "RESET ROLE")
		s.conn.ExecContext(s.pair.Ctx, "DROP SCHEMA IF EXISTS mcv_hidden CASCADE")
		s.conn.ExecContext(s.pair.Ctx, "DROP OWNED BY mcv_smoke_actor")
		s.conn.ExecContext(s.pair.Ctx, "DROP ROLE mcv_smoke_actor")
	}()
	s.run(fixture.Exec("CREATE SCHEMA mcv_hidden", 0))
	s.run(fixture.Exec("CREATE TABLE mcv_hidden.h(a INT,b INT)", 0))
	s.run(fixture.Exec("SET ROLE mcv_smoke_actor", 0))
	s.run(statisticsError("ANALYZE mcv_hidden.h", "42501", "permission denied for schema mcv_hidden"))
	step := statisticsError("SELECT * FROM pg_statistic_ext_data WHERE FALSE", "42501", "permission denied for table pg_statistic_ext_data")
	step.SelectRows = true
	s.run(step)
	s.run(fixture.Exec("RESET ROLE", 0))
}
func TestReferenceStatisticsMCVPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mcv_actor", "mcv_owner", "mcv_group"})
	fixture.SchemaReferenceCleanup(t, "nd_hidden")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
			s := ndistinctScenario{run: run, pg: b.PG, conn: conn, pair: p}
			for _, q := range []string{"CREATE ROLE mcv_actor", "CREATE ROLE mcv_owner", "CREATE ROLE mcv_group", "GRANT USAGE ON SCHEMA public TO mcv_actor,mcv_owner,mcv_group", "CREATE TABLE nd_t(a INT,b INT)", "CREATE STATISTICS nd_s(mcv) ON a,b FROM nd_t", "CREATE SCHEMA nd_hidden", "CREATE TABLE nd_hidden.h(a INT,b INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(1,1),(2,2),(NULL,NULL)", 4))
			run(fixture.Exec("ANALYZE nd_t", 0))
			run(fixture.Exec("INSERT INTO nd_t VALUES(2,2)", 1))
			var notices []fixture.Diagnostic
			_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			arun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, step) }
			arun(fixture.Exec("SET ROLE mcv_actor", 0))
			defer actor.ExecContext(p.Ctx, "RESET ROLE")
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := fixture.Exec("ANALYZE nd_t", 0)
				step.Prepared = prepared
				arun(step)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to analyze "nd_t", skipping it`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				step = statisticsError("ANALYZE nd_hidden.h", "42501", "permission denied for schema nd_hidden")
				step.Prepared = prepared
				arun(step)
			}
			mcvAssert(t, s, "nd_s", mcvFour...)
			run(fixture.Exec("GRANT SELECT ON pg_statistic_ext_data TO mcv_actor", 0))
			held, err := actor.PrepareContext(p.Ctx, mcvSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			checkHeld := func() {
				var data []byte
				if err := held.QueryRowContext(p.Ctx, "nd_s", false).Scan(&data); err != nil {
					t.Fatal(err)
				}
				assertIntMCV(t, decodeMCV(t, data), mcvFour...)
			}
			checkHeld()
			mcvReadOn(t, s, actor, "nd_s", mcvFour...)
			run(fixture.Exec("REVOKE SELECT ON pg_statistic_ext_data FROM mcv_actor", 0))
			var data []byte
			err = held.QueryRowContext(p.Ctx, "nd_s", false).Scan(&data)
			fixture.AssertDiagnostic(t, err, diagnosticMCVData())
			for _, q := range []string{"SELECT * FROM pg_statistic_ext_data", "SELECT * FROM pg_statistic_ext_data WHERE FALSE"} {
				step := statisticsError(q, "42501", "permission denied for table pg_statistic_ext_data")
				step.SelectRows = true
				arun(step)
				arun(fixture.Prepared(step))
			}
			run(fixture.Exec("GRANT SELECT ON pg_statistic_ext_data TO mcv_actor", 0))
			checkHeld()
			run(fixture.Exec("GRANT MAINTAIN ON nd_t TO mcv_actor", 0))
			arun(fixture.Prepared(fixture.Exec("ANALYZE nd_t", 0)))
			mcvReadOn(t, s, actor, "nd_s", mcvFive...)
			run(fixture.Exec("REVOKE MAINTAIN ON nd_t FROM mcv_actor", 0))
			run(fixture.Exec("GRANT MAINTAIN ON nd_t TO mcv_group", 0))
			run(fixture.Exec("GRANT mcv_group TO mcv_actor", 0))
			arun(fixture.Exec("ANALYZE nd_t", 0))
			mcvReadOn(t, s, actor, "nd_s", mcvFive...)
			run(fixture.Exec("ALTER TABLE nd_t OWNER TO mcv_owner", 0))
			run(fixture.Exec("GRANT CREATE ON SCHEMA public TO mcv_owner", 0))
			run(fixture.Exec("REVOKE SELECT ON pg_statistic_ext_data FROM mcv_actor", 0))
			arun(fixture.Exec("RESET ROLE", 0))
			arun(fixture.Exec("SET ROLE mcv_owner", 0))
			arun(fixture.Exec("CREATE STATISTICS owner_s(mcv) ON a,b FROM nd_t", 0))
			step := statisticsError("SELECT * FROM pg_statistic_ext_data WHERE FALSE", "42501", "permission denied for table pg_statistic_ext_data")
			step.SelectRows = true
			arun(step)
			arun(fixture.Exec("RESET ROLE", 0))
		})
	}
}
func diagnosticMCVData() *fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_statistic_ext_data")
	return &d
}
