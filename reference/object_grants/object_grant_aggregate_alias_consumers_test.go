package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAggregateAliasStoredAuthority(t *testing.T) {
	aggregateAliasMatrix(t, []string{"plain", "combined"}, func(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
		aggregateAliasStoredScenario(t, f, order, revoked, shape)
	})
}

func aggregateAliasStoredScenario(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
	t.Helper()
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		// Capture only supported symbolic consumers with default builtin ACLs.
		// Access grants and ownership are established independently after replay.
		run(fixture.Exec("CREATE TABLE og_ai_input(id INTEGER,b BOOLEAN,x DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO og_ai_input VALUES(1,TRUE,1),(2,TRUE,3),(3,FALSE,5),(4,NULL,NULL)", 4))
		body := "SELECT " + f.projection(order, shape) + " FROM og_ai_input"
		run(fixture.Exec("CREATE VIEW og_ai_view AS "+body, 0))
		run(fixture.Exec("CREATE VIEW og_ai_invoker WITH(security_invoker=true) AS "+body, 0))
		var snapshot bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
				t.Fatal(err)
			}
		}
		target := "pg_catalog." + f.names[revoked] + "(" + f.signature + ")"
		lifecycle := func() {
			aggregateAliasAccess(run)
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_other", 0))
			for _, name := range f.names {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+name+"("+f.signature+") TO og_object_other", 0))
			}
			defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog."+f.names[0]+"("+f.signature+")", "pg_catalog."+f.names[1]+"("+f.signature+")"})
			cases := []objectGrantProviderCase{{f.selectStep(body, shape), true, true}}
			for _, view := range []string{"og_ai_view", "og_ai_invoker"} {
				run(fixture.Exec("ALTER VIEW "+view+" OWNER TO og_object_other", 0))
				run(fixture.Exec("GRANT SELECT ON "+view+" TO og_object_reader", 0))
				cases = append(cases, objectGrantProviderCase{f.selectStep("SELECT a,z FROM "+view, shape), true, true})
			}
			objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "aggregate", f.names[revoked], cases, []fixture.Step{aggregateAliasControl(f, revoked, shape)})
		}
		lifecycle()
		if !b.PG {
			// All held handles from the first lifecycle are now closed. Reset
			// also restores default builtin ACLs before importing the snapshot.
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT id,b,x FROM og_ai_input ORDER BY id", []string{"id", "b", "x"}, []string{"INT4", "BOOL", "FLOAT8"}, [][]any{{int64(1), true, float64(1)}, {int64(2), true, float64(3)}, {int64(3), false, float64(5)}, {int64(4), nil, nil}}))
			lifecycle()
		}
	})
}

func TestReferenceObjectGrantAggregateAliasFailureNonpublication(t *testing.T) {
	aggregateAliasMatrix(t, []string{"combined"}, func(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
		aggregateAliasPublicationScenario(t, f, order, revoked, shape)
	})
}

func aggregateAliasPublicationScenario(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
	t.Helper()
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		aggregateAliasInput(run)
		run(fixture.Exec("CREATE TABLE og_ai_sink(id INTEGER,a "+f.sinkType+",z "+f.sinkType+")", 0))
		run(fixture.Exec("INSERT INTO og_ai_sink VALUES(99,NULL,NULL)", 1))
		run(fixture.Exec("GRANT SELECT,INSERT,DELETE ON og_ai_sink TO og_object_reader", 0))
		target := "pg_catalog." + f.names[revoked] + "(" + f.signature + ")"
		defer objectGrantRoutineCleanup(t, p, c, []string{target})
		insert := "INSERT INTO og_ai_sink SELECT 1," + f.projection(order, shape) + " FROM og_ai_input"
		var stmt *sql.Stmt
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			var err error
			stmt, err = c.PrepareContext(p.Ctx, insert)
			if err != nil {
				t.Fatal(err)
			}
		})
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: insert}
		for phase, allowed := range []bool{true, false, true, false} {
			switch phase {
			case 1:
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
			case 2:
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
			case 3:
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for _, retained := range []bool{false, true} {
					s := fixture.Exec(insert, 1)
					if !allowed {
						s = fixture.ImplicitError(insert, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate " + f.names[revoked]})
						s.ErrorStage = "execute"
					}
					if retained {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					} else {
						run(s)
					}
					want := [][]any{{int64(99), nil, nil}}
					if allowed {
						want = [][]any{{int64(1), f.value, f.value}, {int64(99), nil, nil}}
					}
					run(fixture.Select("SELECT id,a,z FROM og_ai_sink ORDER BY id", []string{"id", "a", "z"}, []string{"INT4", f.typ, f.typ}, want))
					if allowed {
						run(fixture.Exec("DELETE FROM og_ai_sink WHERE id=1", 1))
					}
					run(fixture.Select("SELECT id,b,x FROM og_ai_input ORDER BY id", []string{"id", "b", "x"}, []string{"INT4", "BOOL", "FLOAT8"}, [][]any{{int64(1), true, float64(1)}, {int64(2), true, float64(3)}, {int64(3), false, float64(5)}, {int64(4), nil, nil}}))
				}
				run(aggregateAliasControl(f, revoked, shape))
			})
		}
	})
}
