package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func concurrentOwnerDisclosure(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"concurrent_owner", "concurrent_caller"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER concurrent_owner", 0))
			run(fixture.Exec("CREATE USER concurrent_caller", 0))
			run(fixture.Exec("CREATE TABLE dup_base(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO dup_base VALUES(1,10)", 1))
			run(fixture.Exec("GRANT SELECT ON dup_base TO concurrent_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION concurrent_owner", 0))
			fixture.WithPublicSchemaOwner(t, p, b, "concurrent_owner", func() { run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b FROM dup_base", 1)) })
			fixture.WithPublicSchemaOwner(t, p, b, "concurrent_owner", func() { run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a)", 0)) })
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION concurrent_caller", 0))
			q := "REFRESH MATERIALIZED VIEW CONCURRENTLY mv"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("prepare checks permission", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for materialized view mv")))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT MAINTAIN ON mv TO concurrent_caller", 0))
			run(fixture.Exec("INSERT INTO dup_base VALUES(1,20)", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION concurrent_caller", 0))
			d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "mv_u"`)
			d.Detail = "Key (a)=(1) already exists."
			d.Schema = "public"
			d.Table = "mv"
			d.Constraint = "mv_u"
			concurrentRuntimeFailure(t, p, b, held, fixture.ErrorDiagnostic(q, d))
			run(fixture.Select("SELECT current_user AS who", []string{"who"}, []string{"NAME"}, [][]any{{"concurrent_caller"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT a,b FROM mv", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		})
	}
}

func concurrentDuplicatePrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE dup_base(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO dup_base VALUES(1,10),(2,20)", 2))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b FROM dup_base", 2))
			run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a)", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX mv_b_u ON mv(b)", 0))
			run(fixture.Exec("INSERT INTO dup_base VALUES(1,30),(2,20)", 2))
			d := fixture.ControlsReferenceDiagnostic("21000", `new data for materialized view "mv" contains duplicate rows without any null columns`)
			d.Detail = "Row: (2,20)"
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", d))
			s := fixture.Select("SELECT a,b FROM mv", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
			s.Bag = true
			run(s)
			fixture.MaterializedViewPopulation(run, "mv", true)
		})
	}
}
