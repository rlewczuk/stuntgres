package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmForks(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pw_fork_reader"})
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pwCacheInstall(t, p, b, c)
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, fork := range []string{"fsm", "vm", "init"} {
			pwBodyError(t, p, b, c, pwError(pwCall, "22023", "fork \""+fork+"\" does not exist for this relation", "pw_part.heap", "buffer", fork, nil, nil))
		}
		pwRoutes(t, p, b, c, pwCount(pwCall, 0, "pw_part.heap", "buffer", "main", nil, nil))
		bcDDL(t, p, b, c, "CREATE UNLOGGED TABLE pw_part.unlogged(k integer)", "CREATE INDEX unlogged_idx ON pw_part.unlogged(k)", "CREATE UNLOGGED SEQUENCE pw_part.unlogged_seq", "CREATE SEQUENCE pw_part.logged_seq", "CREATE TEMP TABLE pw_temp(k integer)")
		// Existing empty unlogged heap init differs from absent logged/TEMP init.
		pwRoutes(t, p, b, c, pwCount(pwCall, 0, "pw_part.unlogged", "buffer", "init", nil, nil))
		pwBodyError(t, p, b, c, pwError(pwCall, "22023", "starting block number must be between 0 and -1", "pw_part.unlogged", "buffer", "init", int64(0), nil))
		pwRoutes(t, p, b, c, pwCount(pwCall, 0, "pw_part.unlogged", "buffer", "init", nil, nil))
		pwBodyError(t, p, b, c, pwError(pwCall, "22023", "ending block number must be between 0 and -1", "pw_part.unlogged", "buffer", "init", nil, int64(0)))
		pwBodyError(t, p, b, c, pwError(pwCall, "22023", "fork \"init\" does not exist for this relation", "pw_part.heap", "buffer", "init", int64(-1), int64(-1)))
		for _, target := range []string{"pw_part.unlogged_idx", "pw_part.unlogged_seq"} {
			n := pwExtent(t, p, b, c, target, "init", 1)
			if n < 1 { t.Fatal("genuine init baseline", target, n) }
			pwRoutes(t, p, b, c, pwCount(pwCall, n, target, "buffer", "init", nil, nil))
			pwResident(t, p, b, c, target, 3, 0, n-1)
		}
		for _, target := range []string{"pw_part.logged_seq", "pw_temp"} {
			pwBodyError(t, p, b, c, pwError(pwCall, "22023", "fork \"init\" does not exist for this relation", target, "buffer", "init", nil, nil))
		}
		pwInsert(t, p, b, c, "pw_part.heap", 1000)
		bcDDL(t, p, b, c, "VACUUM (FREEZE) pw_part.heap")
		for _, tc := range []struct{ name string; number int64 }{{"main", 0}, {"fsm", 1}, {"vm", 2}} {
			native := int64(1)
			if tc.name == "main" { native = 6 }
			n := pwExtent(t, p, b, c, "pw_part.heap", tc.name, native)
			if n < 1 { t.Fatal("VACUUM must create positive auxiliary witness", tc.name, n) }
			pwRoutes(t, p, b, c, pwCount(pwCall, n, "pw_part.heap", "buffer", tc.name, nil, nil))
			pwResident(t, p, b, c, "pw_part.heap", tc.number, 0, n-1)
			if tc.name != "main" {
				upper := fmt.Sprint(n-1)
				pwBodyError(t, p, b, c, pwError(pwCall, "22023", "starting block number must be between 0 and "+upper, "pw_part.heap", "buffer", tc.name, n, int64(-1)))
				pwBodyError(t, p, b, c, pwError(pwCall, "22023", "ending block number must be between 0 and "+upper, "pw_part.heap", "buffer", tc.name, int64(0), n))
			}
		}
		bcDDL(t, p, b, c, "CREATE INDEX heap_idx ON pw_part.heap(k)")
		bcRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW pw_part.mat AS SELECT k FROM pw_part.heap", 1000))
		for _, target := range []string{"pw_part.heap_idx", "pw_part.logged_seq", "pw_part.mat"} {
			// A single explicit block proves storage-kind admission without
			// pretending compact indexes equal PostgreSQL physical page counts.
			pwRoutes(t, p, b, c, pwCount(pwCall, 1, target, "buffer", "main", int64(0), int64(0)))
		}
		pwInsert(t, p, b, c, "pw_temp", 1)
		pwRoutes(t, p, b, c, pwCount(pwCall, 1, "pw_temp", "buffer", "main", nil, nil))
		if !b.PG && len(pwCoordinates(t, p, b, c, "pw_temp")) != 0 { t.Fatal("TEMP leaked into shared cache") }
		// A held non-main call must use current SELECT authority, not the
		// permission accepted by its earlier successful execution.
		bcDDL(t, p, b, c, "CREATE ROLE pw_fork_reader", "GRANT USAGE ON SCHEMA pw_part TO pw_fork_reader", "GRANT SELECT ON pw_part.heap TO pw_fork_reader")
		q := "SELECT pw_part.pg_prewarm($1::regclass,'buffer','fsm',0,0) AS warmed"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		bcDDL(t, p, b, c, "SET ROLE pw_fork_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pwCount(q, 1, "pw_part.heap"))
		bcDDL(t, p, b, c, "RESET ROLE", "REVOKE SELECT ON pw_part.heap FROM pw_fork_reader", "SET ROLE pw_fork_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pwError(q, "42501", "permission denied for table heap", "pw_part.heap"))
		bcDDL(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pw_part.heap TO pw_fork_reader", "SET ROLE pw_fork_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pwCount(q, 1, "pw_part.heap"))
		bcDDL(t, p, b, c, "RESET ROLE")
	})
}
