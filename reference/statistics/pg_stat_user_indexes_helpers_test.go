package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const userIndexSelect = "SELECT * FROM pg_catalog.pg_stat_user_indexes WHERE indexrelname=$1"

func userIndexRead(ctx context.Context, c *sql.Conn, name string) (indexStatRow, error) {
	var r indexStatRow
	err := c.QueryRowContext(ctx, userIndexSelect, name).Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
	return r, err
}

func userIndexGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	r, err := userIndexRead(p.Ctx, c, name)
	if err != nil {
		t.Fatal(name, err)
	}
	return r
}

func userIndexHeld(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, name string) (indexStatRow, error) {
	t.Helper()
	var r indexStatRow
	err := stmt.QueryRowContext(p.Ctx, name).Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
	return r, err
}

func userIndexShared(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
	fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
	defer c.ExecContext(context.Background(), "ROLLBACK")
	user, all := userIndexGet(t, p, c, name), indexStatGet(t, p, c, name)
	if !user.equal(all) {
		t.Fatal("USER and ALL must share exact provider facts", user, all)
	}
	fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
	return user
}

// Every phase retires its reporter and awaits a separate insert publication
// marker, including EXPLAIN/Prepare-only and sequential zero-delta phases.
func userIndexAccesses(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	return userIndexAccessesIn(t, p, b, c, "public", name)
}

func userIndexAccessesIn(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema, name string) indexStatRow {
	t.Helper()
	marker := name+"_marker"
	sysIndexOwned(t, b, marker)
	fixture.StatisticsCreateTables(t, p, b, c, marker)
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
	expected := [3]int64{}
	q := "SELECT payload FROM "+schema+"."+name+" WHERE n=$1"
	var positive indexStatRow
	for i, phase := range []string{"explain_prepare", "direct", "held", "miss", "sequential"} {
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		indexStatPrefer(t, p, b, w)
		if phase == "sequential" {
			for _, setting := range []string{"enable_seqscan=on", "enable_indexscan=off", "enable_bitmapscan=off"} {
				fixture.StatisticsExec(t, p, b, w, "SET "+setting, 0)
			}
		}
		arg, entries := 2, int64(2)
		want := [][]any{{"a"}, {"b"}}
		if phase == "miss" {
			arg, entries, want = 99, 0, nil
		}
		method := indexStatPlan(t, p, w, q, arg)
		if phase == "sequential" && ((b.PG && !strings.Contains(method, "Seq Scan")) || (!b.PG && !strings.Contains(method, "Access Method: Full Scan"))) {
			t.Fatal("sequential negative requires actual sequential path", method)
		}
		delta := [3]int64{}
		if phase != "sequential" && phase != "explain_prepare" {
			delta = indexStatDelta(t, b, method, 1, entries)
		}
		if phase == "held" || phase == "miss" || phase == "explain_prepare" {
			stmt, err := w.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "explain_prepare" {
				step := fixture.Select(q, []string{"payload"}, []string{"TEXT"}, want, arg)
				step.Bag = true
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: w, Statement: stmt, SQL: q}, b.Recorder, step)
			}
			stmt.Close()
		} else {
			indexStatPayloads(t, p, b, w, q, []string{"a", "b"}, arg)
		}
		for j := range expected {
			expected[j] += delta[j]
		}
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+marker+" VALUES(1)", 1)
		retire()
		fixture.StatisticsAwaitTableCount(t, p, b, marker, "n_tup_ins", int64(i+1))
		indexStatAwait(t, p, b, name+"_hash", expected[0], expected[1], expected[2])
		current := userIndexShared(t, p, b, c, name+"_hash")
		indexStatWant(t, current, expected[0], expected[1], expected[2])
		if phase == "direct" && !b.PG {
			indexStatWant(t, current, 1, 2, 2)
		}
		if phase == "sequential" && !current.equal(positive) {
			t.Fatal("published sequential access changed index facts or instant", positive, current)
		}
		positive = current
	}
	return positive
}
