package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// A real installation/read/ACL/control workflow, not a wrapper around the matrix.
func TestReferencePgBuffercacheSummaryFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_sum_smoke_reader"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		bcsNative(t, p, b, c, capacity, 0, nil, [6]int64{capacity})
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.sum_smoke_a(k integer)", "CREATE TABLE bc_part.sum_smoke_b(k integer)", "CREATE ROLE bc_sum_smoke_reader", "GRANT USAGE ON SCHEMA bc_part TO bc_sum_smoke_reader")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.sum_smoke_a VALUES(1)", 1))
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.sum_smoke_b VALUES(1)", 1))
		q := "SELECT * FROM bc_part.pg_buffercache_summary()"
		st, err := c.PrepareContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readSQL := "SELECT k FROM bc_part.sum_smoke_a"
		read, err := c.PrepareContext(p.Ctx, readSQL)
		fixture.State(t, err, "")
		defer read.Close()
		bcReadHeap(t, p, b, c, readSQL)
		bcReadHeap(t, p, b, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, readSQL)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.sum_smoke_b")
		bcsNative(t, p, b, held, capacity, 2, float64(1.5), [6]int64{capacity - 2, 1, 1})
		actor, err := b.DB.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer actor.Close()
		defer actor.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		bcDDL(t, p, b, actor, "SET SESSION AUTHORIZATION bc_sum_smoke_reader")
		for _, name := range bcsRoutines {
			bcFailure(t, p, b, actor, bcError("SELECT * FROM bc_part."+name+"()", "42501", "permission denied for function "+name), "execute")
		}
		bcDDL(t, p, b, c, "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() TO bc_sum_smoke_reader")
		actorStatement, err := actor.PrepareContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer actorStatement.Close()
		actorHeld := fixture.HeldStatement{Executor: actor, Statement: actorStatement, SQL: q}
		bcsSummaryBounds(t, bcsQuery(t, p, b, actorHeld, q, true, false), capacity, true)
		bcFailure(t, p, b, actor, bcError("SELECT * FROM bc_part.pg_buffercache_usage_counts()", "42501", "permission denied for function pg_buffercache_usage_counts"), "execute")
		bcDDL(t, p, b, c, "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() TO bc_sum_smoke_reader")
		bcsUsageBounds(t, bcsQuery(t, p, b, actor, "SELECT * FROM bc_part.pg_buffercache_usage_counts() ORDER BY usage_count", false, false), capacity)
		bcDDL(t, p, b, c, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() FROM bc_sum_smoke_reader")
		bcRun(t, p, b, actorHeld, bcError(q, "42501", "permission denied for function pg_buffercache_summary"))
		bcDDL(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		actorStatement.Close()
		actor.Close()
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		borrower, err := b.DB.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer old.Close()
		bcMetadata(t, old, false, false, bcsSummaryNames, bcsSummaryTypes)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		bcsExact(t, bcRows(t, old, false, false, bcsSummaryNames, bcsSummaryTypes), [][]any{{int64(2), capacity - 2, int64(0), int64(0), float64(1.5)}})
		borrower.Close()
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bcsNative(t, p, b, held, capacity, 0, nil, [6]int64{capacity})
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.sum_smoke_a")
		bcsNative(t, p, b, held, capacity, 1, float64(1), [6]int64{capacity - 1, 1})
	})
}
