package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Independent REL_18_6 relation reporting vectors, not native observations.
type statsReportingVector [7]int64

var statsReportingColumns = []string{"n_tup_ins", "n_tup_upd", "n_tup_del", "n_live_tup", "n_dead_tup", "n_mod_since_analyze", "n_ins_since_vacuum"}

func statsReportingSQL(table string) string {
	return "SELECT n_tup_ins,n_tup_upd,n_tup_del,n_live_tup,n_dead_tup,n_mod_since_analyze,n_ins_since_vacuum FROM pg_stat_user_tables WHERE relid='public." + table + "'::regclass"
}
func statsReportingRead(ctx context.Context, c *sql.Conn, table string) (statsReportingVector, error) {
	var v statsReportingVector
	err := c.QueryRowContext(ctx, statsReportingSQL(table)).Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6])
	return v, err
}
func statsReportingWant(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, want statsReportingVector) {
	t.Helper()
	row := make([]any, len(want))
	types := make([]string, len(want))
	for i, n := range want {
		row[i] = n
		types[i] = "INT8"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(statsReportingSQL(table), statsReportingColumns, types, [][]any{row}))
}
func statsReportingAwait(t *testing.T, p *fixture.Pair, observer *sql.Conn, table string, want statsReportingVector) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		got, err := statsReportingRead(ctx, observer, table)
		if err != nil {
			t.Fatalf("%s publication last=%v want=%v: %v", table, got, want, err)
		}
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s publication last=%v want=%v: %v", table, got, want, ctx.Err())
		case <-tick.C:
		}
	}
}
func statsReportingConn(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, func()) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	owned := true
	t.Cleanup(func() {
		if owned {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Errorf("reporting rollback cleanup: %v", err)
			}
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	return c, func() {
		statsRetire(t, c)
		owned = false
	}
}
func statsReportingExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, q string, n int64) {
	t.Helper()
	s := fixture.Exec(q, n)
	s.Prepared = prepared
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}
func statsReportingRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, ids []int64) {
	t.Helper()
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}
func statsReportingDone(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, retire func(), table string, want statsReportingVector, rows []int64) {
	t.Helper()
	retire()
	statsReportingAwait(t, p, observer, table, want)
	statsReportingWant(t, p, b, observer, table, want)
	statsReportingRows(t, p, b, observer, table, rows)
}

// PostgreSQL does not call cumulative report_stat while inside a transaction
// block. These fixed successful commands prove ordinary writer idle transitions
// without demanding impossible publication or waiting for a timeout.
func statsReportingInside(t *testing.T, p *fixture.Pair, b fixture.Backend, observer, writer *sql.Conn, table string, want statsReportingVector) {
	t.Helper()
	statsReportingWant(t, p, b, observer, table, want)
	for _, prepared := range []bool{false, true, false} {
		step := fixture.Select("SELECT 1 AS reporting_ready", []string{"reporting_ready"}, []string{"INT4"}, [][]any{{int64(1)}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, writer, b.Recorder, step)
		statsReportingWant(t, p, b, observer, table, want)
	}
}

func statsReportingMarker(t *testing.T, p *fixture.Pair, b fixture.Backend, observer, writer *sql.Conn, table string, prepared bool) {
	t.Helper()
	for _, s := range []fixture.Step{fixture.Exec("SAVEPOINT reporting_marker", 0), fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2), fixture.Exec("ROLLBACK TO reporting_marker", 0), fixture.Exec("RELEASE reporting_marker", 0)} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, writer, b.Recorder, s)
	}
	statsReportingInside(t, p, b, observer, writer, table, statsReportingVector{})
}

func TestReferenceStatsSnapshotTransactionReporting(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
		statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, mixed := range []bool{false, true} {
					name := fmt.Sprintf("stats_tx_%t_%t_%t", prepared, commit, mixed)
					marker := name + "_marker"
					statsCreate(t, p, b, observer, name, marker)
					var before statsReportingVector
					if mixed {
						statsPublish(t, p, b, name, 3, fixture.Exec("INSERT INTO "+name+" VALUES(1),(2),(3)", 3))
						before = statsReportingVector{3, 0, 0, 3, 0, 3, 3}
					}
					writer, retireWriter := statsReportingConn(t, p, b)
					run := func(q string, n int64) { statsReportingExec(t, p, b, writer, prepared, q, n) }
					run("BEGIN", 0)
					if mixed {
						run("INSERT INTO "+name+" VALUES(4)", 1)
						run("UPDATE "+name+" SET id=11 WHERE id=1", 1)
						run("DELETE FROM "+name+" WHERE id=2", 1)
					} else {
						run("INSERT INTO "+name+" VALUES(1),(2)", 2)
					}
					statsReportingWant(t, p, b, observer, name, before)
					statsReportingMarker(t, p, b, observer, writer, marker, prepared)
					statsReportingWant(t, p, b, observer, name, before)
					end := "ROLLBACK"
					want := statsReportingVector{2, 0, 0, 0, 2, 0, 2}
					var rows []int64
					if commit {
						end = "COMMIT"
						want = statsReportingVector{2, 0, 0, 2, 0, 2, 2}
						rows = []int64{1, 2}
					}
					if mixed {
						want = statsReportingVector{4, 1, 1, 3, 2, 3, 4}
						rows = []int64{1, 2, 3}
						if commit {
							want[5] = 6
							rows = []int64{3, 4, 11}
						}
					}
					run(end, 0)
					statsReportingDone(t, p, b, observer, retireWriter, name, want, rows)
					markerWant := statsReportingVector{2, 0, 0, 0, 2, 0, 2}
					statsReportingAwait(t, p, observer, marker, markerWant)
					statsReportingWant(t, p, b, observer, marker, markerWant)
				}
			}
		}
	})
}

func TestReferenceStatsSnapshotPreparedReporting(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
		statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, truncate := range []bool{false, true} {
					name := fmt.Sprintf("stats_pr_%t_%t_%t", prepared, commit, truncate)
					marker := name + "_marker"
					statsCreate(t, p, b, observer, name, marker)
					writer, retireWriter := statsReportingConn(t, p, b)
					run := func(q string, n int64) { statsReportingExec(t, p, b, writer, prepared, q, n) }
					run("BEGIN", 0)
					run("INSERT INTO "+name+" VALUES(1),(2)", 2)
					if truncate {
						run("TRUNCATE "+name, 0)
						run("INSERT INTO "+name+" VALUES(3)", 1)
					}
					statsReportingWant(t, p, b, observer, name, statsReportingVector{})
					statsReportingMarker(t, p, b, observer, writer, marker, prepared)
					statsReportingWant(t, p, b, observer, name, statsReportingVector{})
					gid := name + "_gid"
					fixture.PreparedReferenceCleanup(t, b, gid)
					literal := fixture.PreparedReferenceLiteral(gid)
					run("PREPARE TRANSACTION "+literal, 0)
					retireWriter()
					// Exit can publish the completed marker child, never transferred target I/U/D.
					statsReportingAwait(t, p, observer, marker, statsReportingVector{2, 0, 0, 0, 2, 0, 2})
					statsReportingWant(t, p, b, observer, name, statsReportingVector{})
					finisher, retireFinisher := statsReportingConn(t, p, b)
					end := "ROLLBACK PREPARED "
					want := statsReportingVector{2, 0, 0, 0, 2, 0, 2}
					var rows []int64
					if commit {
						end = "COMMIT PREPARED "
						want = statsReportingVector{2, 0, 0, 2, 0, 2, 2}
						rows = []int64{1, 2}
						if truncate {
							want = statsReportingVector{1, 0, 0, 1, 0, 1, 1}
							rows = []int64{3}
						}
					}
					statsReportingExec(t, p, b, finisher, prepared, end+literal, 0)
					statsReportingDone(t, p, b, observer, retireFinisher, name, want, rows)
				}
			}
		}
	})
}

type statsReportingCommand struct {
	sql      string
	affected int64
}

func TestReferenceStatsSnapshotTruncateReporting(t *testing.T) {
	cases := []struct {
		name   string
		before []statsReportingCommand
		end    string
		seed   bool
		child  bool
		want   statsReportingVector
		rows   []int64
	}{
		{name: "commit", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}}, end: "COMMIT", want: statsReportingVector{1, 0, 0, 1, 0, 1, 1}, rows: []int64{3}},
		{name: "abort", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}}, end: "ROLLBACK", want: statsReportingVector{2, 0, 0, 0, 2, 0, 2}},
		{name: "repeat_commit", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}}, end: "COMMIT", want: statsReportingVector{3, 0, 0, 3, 0, 3, 3}, rows: []int64{4, 5, 6}},
		{name: "repeat_abort", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}}, end: "ROLLBACK", want: statsReportingVector{2, 0, 0, 0, 2, 0, 2}},
		{name: "child_rollback", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"SAVEPOINT child", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(7)", 1}, {"ROLLBACK TO child", 0}}, child: true, end: "COMMIT", want: statsReportingVector{5, 0, 0, 2, 3, 2, 5}, rows: []int64{1, 2}},
		{name: "child_rollback_abort", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"SAVEPOINT child", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(7)", 1}, {"ROLLBACK TO child", 0}}, child: true, end: "ROLLBACK", want: statsReportingVector{5, 0, 0, 0, 5, 0, 5}},
		{name: "release_commit", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"SAVEPOINT child", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(7)", 1}, {"RELEASE child", 0}}, end: "COMMIT", want: statsReportingVector{1, 0, 0, 1, 0, 1, 1}, rows: []int64{7}},
		{name: "release_abort", before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"SAVEPOINT child", 0}, {"INSERT INTO %s VALUES(4),(5),(6)", 3}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(7)", 1}, {"RELEASE child", 0}}, end: "ROLLBACK", want: statsReportingVector{2, 0, 0, 0, 2, 0, 2}},
		{name: "history_commit", seed: true, before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}}, end: "COMMIT", want: statsReportingVector{5, 0, 0, 1, 0, 5, 1}, rows: []int64{3}},
		{name: "history_abort", seed: true, before: []statsReportingCommand{{"INSERT INTO %s VALUES(1),(2)", 2}, {"TRUNCATE %s", 0}, {"INSERT INTO %s VALUES(3)", 1}}, end: "ROLLBACK", want: statsReportingVector{6, 0, 0, 4, 2, 4, 6}, rows: []int64{11, 12, 13, 14}},
	}
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
		statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			for _, tc := range cases {
				name := fmt.Sprintf("stats_tr_%s_%t", tc.name, prepared)
				statsCreate(t, p, b, observer, name)
				if tc.seed {
					statsPublish(t, p, b, name, 4, fixture.Exec("INSERT INTO "+name+" VALUES(11),(12),(13),(14)", 4))
				}
				writer, retireWriter := statsReportingConn(t, p, b)
				statsReportingExec(t, p, b, writer, prepared, "BEGIN", 0)
				for _, cmd := range tc.before {
					query := cmd.sql
					if cmd.sql != "SAVEPOINT child" && cmd.sql != "ROLLBACK TO child" && cmd.sql != "RELEASE child" {
						query = fmt.Sprintf(query, name)
					}
					statsReportingExec(t, p, b, writer, prepared, query, cmd.affected)
				}
				if tc.child {
					statsReportingInside(t, p, b, observer, writer, name, statsReportingVector{})
				}
				statsReportingExec(t, p, b, writer, prepared, tc.end, 0)
				statsReportingDone(t, p, b, observer, retireWriter, name, tc.want, tc.rows)
			}
			// Reuse one real PL command scope across both truncates. The empty
			// protected block also transfers the prefix into its actual owner.
			for _, rollback := range []bool{false, true} {
				name := fmt.Sprintf("stats_tr_pl_%t_%t", prepared, rollback)
				statsCreate(t, p, b, observer, name)
				writer, retire := statsReportingConn(t, p, b)
				end := ""
				want := statsReportingVector{1, 0, 0, 1, 0, 1, 1}
				rows := []int64{7}
				if rollback {
					end = "ROLLBACK;"
					want = statsReportingVector{2, 0, 0, 0, 2, 0, 2}
					rows = nil
				}
				query := fmt.Sprintf("DO $$BEGIN INSERT INTO %[1]s VALUES(1),(2); TRUNCATE %[1]s; INSERT INTO %[1]s VALUES(3); BEGIN PERFORM 1; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; INSERT INTO %[1]s VALUES(4),(5),(6); TRUNCATE %[1]s; INSERT INTO %[1]s VALUES(7); %[2]s END$$", name, end)
				statsReportingExec(t, p, b, writer, prepared, query, 0)
				statsReportingDone(t, p, b, observer, retire, name, want, rows)
			}
		}
	})
}

func TestReferenceStatsSnapshotAbortReporting(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
		statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				name := fmt.Sprintf("stats_ab_%t_%t", prepared, commit)
				statsCreate(t, p, b, observer, name)
				writer, retireWriter := statsReportingConn(t, p, b)
				run := func(q string, n int64) { statsReportingExec(t, p, b, writer, prepared, q, n) }
				run("BEGIN", 0)
				run("INSERT INTO "+name+" VALUES(1),(2),(3)", 3)
				run("SAVEPOINT child", 0)
				run("INSERT INTO "+name+" VALUES(4),(5)", 2)
				run("ROLLBACK TO child", 0)
				run("INSERT INTO "+name+" VALUES(6)", 1)
				run("ROLLBACK TO child", 0)
				statsReportingInside(t, p, b, observer, writer, name, statsReportingVector{})
				end := "ROLLBACK"
				want := statsReportingVector{6, 0, 0, 0, 6, 0, 6}
				var rows []int64
				if commit {
					end = "COMMIT"
					want = statsReportingVector{6, 0, 0, 3, 3, 3, 6}
					rows = []int64{1, 2, 3}
				}
				run(end, 0)
				statsReportingDone(t, p, b, observer, retireWriter, name, want, rows)
			}
		}
		for _, truncate := range []bool{false, true} {
			name := fmt.Sprintf("stats_exception_%t", truncate)
			statsCreate(t, p, b, observer, name)
			writer, retireWriter := statsReportingConn(t, p, b)
			body := "BEGIN BEGIN INSERT INTO " + name + " VALUES(1); RAISE EXCEPTION 'discard child'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; INSERT INTO " + name + " VALUES(2); END"
			want := statsReportingVector{2, 0, 0, 1, 1, 1, 2}
			rows := []int64{2}
			if truncate {
				body = "BEGIN INSERT INTO " + name + " VALUES(1),(2); BEGIN INSERT INTO " + name + " VALUES(4),(5),(6); TRUNCATE " + name + "; INSERT INTO " + name + " VALUES(7); RAISE EXCEPTION 'discard child truncate'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END"
				want = statsReportingVector{5, 0, 0, 2, 3, 2, 5}
				rows = []int64{1, 2}
			}
			statsReportingExec(t, p, b, writer, true, "DO $$"+body+"$$", 0)
			statsReportingDone(t, p, b, observer, retireWriter, name, want, rows)
		}
		statsCreate(t, p, b, observer, "stats_abort_vacuum")
		statsPublish(t, p, b, "stats_abort_vacuum", 2, fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stats_abort_vacuum VALUES(1),(2)", 2), fixture.Exec("ROLLBACK", 0))
		statsReportingWant(t, p, b, observer, "stats_abort_vacuum", statsReportingVector{2, 0, 0, 0, 2, 0, 2})
		vacuum, retireVacuum := statsReportingConn(t, p, b)
		statsExec(t, p, b, vacuum, "VACUUM stats_abort_vacuum", 0)
		statsReportingDone(t, p, b, observer, retireVacuum, "stats_abort_vacuum", statsReportingVector{2, 0, 0, 0, 0, 0, 0}, nil)
		statsPublish(t, p, b, "stats_abort_vacuum", 3, fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stats_abort_vacuum VALUES(3)", 1), fixture.Exec("ROLLBACK", 0))
		statsReportingWant(t, p, b, observer, "stats_abort_vacuum", statsReportingVector{3, 0, 0, 0, 1, 0, 1})
	})
}

func statsReportingOpenSmoke(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
	t.Helper()
	statsCreate(t, p, b, observer, "stats_smoke_open", "stats_smoke_marker")
	writer, retire := statsReportingConn(t, p, b)
	statsReportingExec(t, p, b, writer, false, "BEGIN", 0)
	statsReportingExec(t, p, b, writer, true, "INSERT INTO stats_smoke_open VALUES(1),(2)", 2)
	statsReportingInside(t, p, b, observer, writer, "stats_smoke_open", statsReportingVector{})
	statsReportingMarker(t, p, b, observer, writer, "stats_smoke_marker", true)
	statsReportingWant(t, p, b, observer, "stats_smoke_open", statsReportingVector{})
	statsReportingExec(t, p, b, writer, true, "COMMIT", 0)
	statsReportingDone(t, p, b, observer, retire, "stats_smoke_open", statsReportingVector{2, 0, 0, 2, 0, 2, 2}, []int64{1, 2})
	marker := statsReportingVector{2, 0, 0, 0, 2, 0, 2}
	statsReportingAwait(t, p, observer, "stats_smoke_marker", marker)
	statsReportingWant(t, p, b, observer, "stats_smoke_marker", marker)
}
