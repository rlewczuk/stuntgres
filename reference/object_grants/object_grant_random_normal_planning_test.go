package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRandomNormalDeclarationAuthority(t *testing.T) {
	for _, tc := range []struct{ name, query, equivalent string }{
		{"zero", "SELECT random_normal() AS v", "SELECT random_normal(0,1) AS v"},
		{"one", "SELECT random_normal(5) AS v", "SELECT random_normal(5,1) AS v"},
		{"two", "SELECT random_normal(5,0) AS v", "SELECT random_normal(5,0) AS v"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog.random_normal(double precision,double precision)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target, "pg_catalog.abs(integer)"})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_rp_normal_source(mean double precision,stddev double precision)", 0))
				run(fixture.Exec("INSERT INTO og_rp_normal_source VALUES(5,0)", 1))
				run(fixture.Exec("GRANT SELECT ON og_rp_normal_source TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				denied := objectGrantRandomPlanningError(tc.query, "42501", "permission denied for function random_normal")
				rowQuery := "SELECT random_normal(mean,stddev) AS v FROM og_rp_normal_source"
				rowDenied := objectGrantRandomPlanningError(rowQuery, "42501", "permission denied for function random_normal")
				var stmt, rowStmt *sql.Stmt
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					rowStmt, err = c.PrepareContext(p.Ctx, rowQuery)
					if err != nil {
						stmt.Close()
						t.Fatal(err)
					}
					run(denied)
					for _, query := range []string{
						"SELECT random_normal(NULL::double precision) AS v",
						"SELECT random_normal(NULL::double precision,1) AS v",
						"SELECT random_normal(0,NULL::double precision) AS v",
					} {
						want := fixture.Select(query, []string{"v"}, []string{"FLOAT8"}, [][]any{{nil}})
						run(want)
						nullStmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil { t.Fatal(err) }
						func() {
							defer nullStmt.Close()
							held := fixture.HeldStatement{Executor: c, Statement: nullStmt, SQL: query}
							for range 2 { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) }
						}()
					}
				})
				defer stmt.Close()
				defer rowStmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				rowHeld := fixture.HeldStatement{Executor: c, Statement: rowStmt, SQL: rowQuery}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
					fixture.RunStep(t, p.Ctx, rowHeld, b.Recorder, rowDenied)
				})
				// An erased outer invocation does not erase required immutable-child admission.
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
				childQuery := "SELECT random_normal(NULL::double precision,abs(-1::integer)) AS v"
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(objectGrantRandomPlanningError(childQuery, "42501", "permission denied for function abs"))
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(fixture.Select(childQuery, []string{"v"}, []string{"FLOAT8"}, [][]any{{nil}}))
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					reseed := func() {
						run(fixture.Select("SELECT setseed(0.25)", []string{"setseed"}, []string{"VOID"}, [][]any{{""}}))
					}
					for _, route := range []string{"consumer", "held_first", "held_again"} {
						reseed()
						want := objectGrantRandomPlanningFinite(t, p, c, tc.equivalent)
						reseed()
						var executor fixture.Executor = c
						var fresh *sql.Stmt
						if route != "consumer" {
							executor = held
						} else if prepared {
							var err error
							fresh, err = c.PrepareContext(p.Ctx, tc.query)
							if err != nil { t.Fatal(err) }
							executor = fixture.HeldStatement{Executor: c, Statement: fresh, SQL: tc.query}
						}
						got := objectGrantRandomPlanningFinite(t, p, executor, tc.query)
						if fresh != nil { fresh.Close() }
						if got != want || (tc.name == "two" && got != 5) {
							t.Fatalf("%s: full/default own-reseed result %v, want %v", route, got, want)
						}
					}
					fixture.RunStep(t, p.Ctx, rowHeld, b.Recorder, fixture.Select(rowQuery, []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(5)}}))
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(denied)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
					fixture.RunStep(t, p.Ctx, rowHeld, b.Recorder, rowDenied)
				})
			})
		})
	}
}
