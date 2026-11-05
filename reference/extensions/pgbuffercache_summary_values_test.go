package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func bcsSummaryBounds(t *testing.T, rows [][]any, capacity int64, populated bool) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatal("summary must contain one row", rows)
	}
	r := rows[0]
	for i := range 4 {
		if r[i] == nil || r[i].(int64) < 0 {
			t.Fatal("summary nonnegative integer", r)
		}
	}
	used, unused := r[0].(int64), r[1].(int64)
	if used+unused != capacity || r[2].(int64) > used || r[3].(int64) > used || (populated && used == 0) {
		t.Fatal("summary capacity/dirty/pinned/positive bounds", r, capacity)
	}
	if used == 0 {
		if r[4] != nil {
			t.Fatal("empty average must be NULL", r)
		}
	} else {
		avg, ok := r[4].(float64)
		if !ok || math.IsNaN(avg) || math.IsInf(avg, 0) || avg < 0 || avg > 5 {
			t.Fatal("populated finite usage average", r)
		}
	}
}

func bcsUsageBounds(t *testing.T, rows [][]any, capacity int64) {
	t.Helper()
	if len(rows) != 6 {
		t.Fatal("six usage buckets including empty buckets", rows)
	}
	var total int64
	for i, r := range rows {
		for _, cell := range r {
			if cell == nil {
				t.Fatal("NULL usage bucket cell", r)
			}
		}
		if r[0] != int64(i) || r[1].(int64) < 0 || r[2].(int64) < 0 || r[3].(int64) < 0 || r[2].(int64) > r[1].(int64) || r[3].(int64) > r[1].(int64) {
			t.Fatal("ordered usage domain and per-bucket bounds", rows)
		}
		total += r[1].(int64)
	}
	if total != capacity {
		t.Fatal("buckets count ALL capacity, not only used buffers", total, capacity)
	}
}

func bcsNative(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, capacity, used int64, avg any, buckets [6]int64) {
	t.Helper()
	summary := bcsQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_summary()", true, false)
	usage := bcsQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_usage_counts() ORDER BY usage_count", false, false)
	bcsSummaryBounds(t, summary, capacity, used > 0)
	bcsUsageBounds(t, usage, capacity)
	if b.PG {
		// Independent physical calls may race background activity. Neither
		// native counts nor cross-call equality is a PostgreSQL golden.
		return
	}
	bcsExact(t, summary, [][]any{{used, capacity - used, int64(0), int64(0), avg}})
	want := make([][]any, 6)
	for i, n := range buckets {
		want[i] = []any{int64(i), n, int64(0), int64(0)}
	}
	bcsExact(t, usage, want)
}

func TestReferencePgBuffercacheSummaryValues(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE EXTENSION tsm_system_rows SCHEMA bc_part")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION tsm_system_rows CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		capacity := bcCapacity(t, p, c)
		bcsNative(t, p, b, c, capacity, 0, nil, [6]int64{capacity})
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.summary_a(k integer)", "CREATE TABLE bc_part.summary_b(k integer)", "CREATE TABLE bc_part.summary_empty(k integer)")
		for _, name := range []string{"summary_a", "summary_b"} {
			bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part."+name+" VALUES(1)", 1))
		}
		q := "SELECT * FROM bc_part.pg_buffercache_summary()"
		st, err := c.PrepareContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		bcsNative(t, p, b, c, capacity, 0, nil, [6]int64{capacity})
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.summary_a")
		bcRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT k FROM bc_part.summary_a", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.summary_b")
		for range 3 {
			bcsNative(t, p, b, held, capacity, 2, float64(1.5), [6]int64{capacity - 2, 1, 1})
		}
		for range 7 {
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.summary_a")
		}
		bcsNative(t, p, b, held, capacity, 2, float64(3), [6]int64{capacity - 2, 1, 0, 0, 0, 1})
		bcBoth(t, p, b, c, fixture.Select("SELECT k FROM bc_part.summary_empty", []string{"k"}, []string{"INT4"}, nil))
		bcBoth(t, p, b, c, fixture.Select("SELECT k FROM bc_part.summary_b TABLESAMPLE bc_part.system_rows(0)", []string{"k"}, []string{"INT4"}, nil))
		bcsNative(t, p, b, held, capacity, 2, float64(3), [6]int64{capacity - 2, 1, 0, 0, 0, 1})
		if !b.PG {
			// Quiescent native pages are an additional independent occupancy
			// witness, not the implementation of either aggregate fixture.
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS used FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL", []string{"used"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
	})
}
