package reference_test

import (
	"context"
	"sort"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticExtDataDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, prepared bool, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_statistic_ext_data"))
	s.SelectRows, s.ErrorStage, s.Prepared, s.Args = true, "execute", prepared, args
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGStatisticExtDataPrivileges(t *testing.T) {
	statisticExtDataACLScenario(t, false)
}

// Both entry points use the same current-authority assertions. The smoke has
// one built object and one unbuilt object; the privilege matrix additionally
// proves that SELECT grants visibility to a different owner's built object.
func statisticExtDataACLScenario(t *testing.T, smoke bool) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"stxd_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string, count int64) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, count)) }
			run(admin, "CREATE ROLE stxd_reader", 0)
			// Install exact catalog ACL repair before the first catalog GRANT.
			defer func() {
				if _, err := admin.ExecContext(context.Background(), "REVOKE SELECT ON pg_catalog.pg_statistic_ext_data FROM stxd_reader"); err != nil {
					t.Error(err)
				}
			}()
			run(admin, "GRANT USAGE,CREATE ON SCHEMA public TO stxd_reader", 0)
			run(actor, "SET ROLE stxd_reader", 0)
			defer actor.ExecContext(context.Background(), "RESET ROLE")
			query := "SELECT * FROM pg_catalog.pg_statistic_ext_data WHERE stxoid=$1 OR stxoid=$2 ORDER BY stxoid"
			falseQuery := "SELECT * FROM pg_catalog.pg_statistic_ext_data WHERE FALSE"
			stmt, err := actor.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			falseStmt, err := actor.PrepareContext(p.Ctx, falseQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer falseStmt.Close()
			held := fixture.HeldStatement{actor, stmt, query}
			heldFalse := fixture.HeldStatement{actor, falseStmt, falseQuery}
			args := []any{int64(0), int64(0)}
			deny := func() {
				for _, e := range []fixture.Executor{actor, held} {
					statisticExtDataDenied(t, p, b, e, query, false, args...)
				}
				for _, e := range []fixture.Executor{actor, heldFalse} {
					statisticExtDataDenied(t, p, b, e, falseQuery, false)
				}
				statisticExtDataDenied(t, p, b, actor, query, true, args...)
				statisticExtDataDenied(t, p, b, actor, falseQuery, true)
				statisticExtDataDenied(t, p, b, actor, "SELECT * FROM pg_statistic_ext_data WHERE FALSE", false)
			}
			deny() // Ordinary reader, no definitions or collected facts.
			for _, q := range []string{"CREATE TABLE stxd_actor_t(a INT,b INT)", "CREATE STATISTICS stxd_actor_s(ndistinct) ON a,b FROM stxd_actor_t"} {
				run(actor, q, 0)
			}
			run(actor, "INSERT INTO stxd_actor_t VALUES(1,1),(1,1),(2,2)", 3)
			for _, q := range []string{"CREATE TABLE stxd_other_t(a INT,b INT)", "CREATE STATISTICS stxd_other_s(ndistinct) ON a,b FROM stxd_other_t"} {
				run(admin, q, 0)
			}
			run(admin, "INSERT INTO stxd_other_t VALUES(7,7),(7,7)", 2)
			id := statisticExtDataID(t, admin, "stxd_actor_s", "stxd_actor_t")
			other := statisticExtDataID(t, admin, "stxd_other_s", "stxd_other_t")
			args = []any{id, other}
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, statisticExtDataStep(b.PG, query, nil, args...))
			deny() // Actual statistics/table ownership does not authorize DATA.
			run(actor, "ANALYZE stxd_actor_t", 0)
			want := [][]any{{id, false, `{"1, 2": 2}`, nil, nil, nil}}
			if !smoke {
				run(admin, "ANALYZE stxd_other_t", 0)
				want = append(want, []any{other, false, `{"1, 2": 1}`, nil, nil, nil})
			}
			sort.Slice(want, func(i, j int) bool { return want[i][0].(int64) < want[j][0].(int64) })
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, statisticExtDataStep(b.PG, query, want, args...))
			if smoke {
				fixture.RunStep(t, p.Ctx, admin, b.Recorder, statisticExtDataStep(b.PG, "SELECT * FROM pg_catalog.pg_statistic_ext_data WHERE stxoid=$1", nil, other))
			}
			deny() // Collection does not weaken the independent catalog ACL.
			allow := func() {
				for _, e := range []fixture.Executor{actor, held} {
					fixture.RunStep(t, p.Ctx, e, b.Recorder, statisticExtDataStep(b.PG, query, want, args...))
				}
				for _, e := range []fixture.Executor{actor, heldFalse} {
					fixture.RunStep(t, p.Ctx, e, b.Recorder, statisticExtDataStep(b.PG, falseQuery, nil))
				}
				s := statisticExtDataStep(b.PG, query, want, args...)
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
				s = statisticExtDataStep(b.PG, falseQuery, nil)
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, statisticExtDataStep(b.PG, "SELECT * FROM pg_statistic_ext_data WHERE stxoid=$1 OR stxoid=$2 ORDER BY stxoid", want, args...))
			}
			run(admin, "GRANT SELECT ON pg_catalog.pg_statistic_ext_data TO stxd_reader", 0)
			allow()
			run(admin, "REVOKE SELECT ON pg_catalog.pg_statistic_ext_data FROM stxd_reader", 0)
			deny()
			run(admin, "GRANT SELECT ON pg_catalog.pg_statistic_ext_data TO stxd_reader", 0)
			allow()
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Select("SELECT a,b FROM stxd_actor_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}}))
		})
	}
}
