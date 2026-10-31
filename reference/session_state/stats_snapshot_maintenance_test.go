package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func statsMaintenanceWant(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, count int64, vector statsReportingVector) {
	t.Helper()
	statsReportingWant(t, p, b, c, name, vector)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT analyze_count,last_analyze IS NOT NULL AS analyzed FROM pg_stat_user_tables WHERE relid='public."+name+"'::regclass", []string{"analyze_count", "analyzed"}, []string{"INT8", "BOOL"}, [][]any{{count, count != 0}}))
}

func TestReferenceStatsSnapshotMaintenanceReporting(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
		statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				name := fmt.Sprintf("stats_maintenance_%t_%t", prepared, commit)
				statsCreate(t, p, b, observer, name)
				statsPublish(t, p, b, name, 2, fixture.Exec("INSERT INTO "+name+" VALUES(1),(2)", 2))
				writer, retire := statsReportingConn(t, p, b)
				snapshot, retireSnapshot := statsReportingConn(t, p, b)
				statsExec(t, p, b, snapshot, "SET stats_fetch_consistency=snapshot", 0)
				statsExec(t, p, b, snapshot, "BEGIN", 0)
				baseline := statsReportingVector{2, 0, 0, 2, 0, 2, 2}
				statsMaintenanceWant(t, p, b, snapshot, name, 0, baseline)
				stamp := statsTimestamp(t, p, snapshot, nil)
				if !stamp.Valid {
					t.Fatal("snapshot capture missing")
				}
				statsReportingExec(t, p, b, writer, prepared, "BEGIN", 0)
				statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
				reported := statsReportingVector{2, 0, 0, 2, 0, 0, 2}
				// Completion, not terminal retirement or a publication poll, is
				// this source-specific shared-report boundary.
				statsMaintenanceWant(t, p, b, observer, name, 1, reported)
				statsMaintenanceWant(t, p, b, snapshot, name, 0, baseline)
				if got := statsTimestamp(t, p, snapshot, nil); !got.Valid || !got.Time.Equal(stamp.Time) {
					t.Fatal("maintenance invalidated snapshot", got, stamp)
				}
				statsClear(t, p, b, snapshot)
				statsNoTimestamp(t, p, snapshot, nil)
				statsMaintenanceWant(t, p, b, snapshot, name, 1, reported)
				rebuilt := statsTimestamp(t, p, snapshot, nil)
				if !rebuilt.Valid || rebuilt.Time.Before(stamp.Time) {
					t.Fatal("clear capture timestamp", rebuilt, stamp)
				}
				statsReportingExec(t, p, b, writer, prepared, "INSERT INTO "+name+" VALUES(3),(4)", 2)
				statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
				statsMaintenanceWant(t, p, b, observer, name, 2, reported)
				statsMaintenanceWant(t, p, b, snapshot, name, 1, reported)
				end := "ROLLBACK"
				want := statsReportingVector{4, 0, 0, 2, 2, 0, 4}
				rows := []int64{1, 2}
				if commit {
					end = "COMMIT"
					want = statsReportingVector{4, 0, 0, 4, 0, 2, 4}
					rows = []int64{1, 2, 3, 4}
				}
				statsReportingExec(t, p, b, writer, prepared, end, 0)
				statsReportingDone(t, p, b, observer, retire, name, want, rows)
				statsMaintenanceWant(t, p, b, observer, name, 2, want)
				statsExec(t, p, b, snapshot, "ROLLBACK", 0)
				retireSnapshot()
			}
			name := fmt.Sprintf("stats_maintenance_policy_%t", prepared)
			statsCreate(t, p, b, observer, name)
			statsPublish(t, p, b, name, 2, fixture.Exec("INSERT INTO "+name+" VALUES(1),(2)", 2))
			writer, retire := statsReportingConn(t, p, b)
			for _, q := range []string{"BEGIN", "ANALYZE " + name, "SAVEPOINT report_child", "ANALYZE " + name, "ROLLBACK TO report_child", "RELEASE report_child"} {
				statsReportingExec(t, p, b, writer, prepared, q, 0)
			}
			statsMaintenanceWant(t, p, b, observer, name, 2, statsReportingVector{2, 0, 0, 2, 0, 0, 2})
			statsReportingExec(t, p, b, writer, prepared, "SET LOCAL track_counts=off", 0)
			statsReportingExec(t, p, b, writer, prepared, "INSERT INTO "+name+" VALUES(9)", 1)
			statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
			statsMaintenanceWant(t, p, b, observer, name, 2, statsReportingVector{2, 0, 0, 2, 0, 0, 2})
			fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Select("SELECT reltuples::bigint AS sampled FROM pg_class WHERE oid='public."+name+"'::regclass", []string{"sampled"}, []string{"INT8"}, [][]any{{int64(3)}}))
			statsReportingExec(t, p, b, writer, prepared, "SET LOCAL track_counts=on", 0)
			statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
			statsMaintenanceWant(t, p, b, observer, name, 3, statsReportingVector{2, 0, 0, 3, 0, 0, 2})
			statsReportingExec(t, p, b, writer, prepared, "SET LOCAL track_counts=off", 0)
			statsReportingExec(t, p, b, writer, prepared, "COMMIT", 0)
			retire()
			statsMaintenanceWant(t, p, b, observer, name, 3, statsReportingVector{2, 0, 0, 3, 0, 0, 2})
			statsReportingRows(t, p, b, observer, name, []int64{1, 2, 9})
			for _, commit := range []bool{false, true} {
				name := fmt.Sprintf("stats_maintenance_new_%t_%t", prepared, commit)
				writer, retire := statsReportingConn(t, p, b)
				statsExec(t, p, b, writer, "SET stats_fetch_consistency=none", 0)
				for _, q := range []string{"BEGIN", "CREATE TABLE " + name + "(id integer)"} {
					statsReportingExec(t, p, b, writer, prepared, q, 0)
				}
				statsReportingExec(t, p, b, writer, prepared, "INSERT INTO "+name+" VALUES(1),(2)", 2)
				statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
				statsMaintenanceWant(t, p, b, writer, name, 1, statsReportingVector{})
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT count(*) AS visible FROM pg_class WHERE relname='"+name+"'", []string{"visible"}, []string{"INT8"}, [][]any{{int64(0)}}))
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				statsReportingExec(t, p, b, writer, prepared, end, 0)
				retire()
				if commit {
					want := statsReportingVector{2, 0, 0, 2, 0, 2, 2}
					statsReportingAwait(t, p, observer, name, want)
					statsMaintenanceWant(t, p, b, observer, name, 1, want)
				} else {
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT count(*) AS visible FROM pg_class WHERE relname='"+name+"'", []string{"visible"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
			statsMaintenanceColumnPartition(t, p, b, observer, prepared)
			statsMaintenanceRecreate(t, p, b, observer, prepared)
		}
	})
}

func statsMaintenanceOpenSmoke(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
	t.Helper()
	const name = "stats_smoke_maintenance"
	statsCreate(t, p, b, observer, name)
	statsPublish(t, p, b, name, 2, fixture.Exec("INSERT INTO "+name+" VALUES(1),(2)", 2))
	snapshot, retireSnapshot := statsReportingConn(t, p, b)
	statsExec(t, p, b, snapshot, "SET stats_fetch_consistency=snapshot", 0)
	statsExec(t, p, b, snapshot, "BEGIN", 0)
	statsMaintenanceWant(t, p, b, snapshot, name, 0, statsReportingVector{2, 0, 0, 2, 0, 2, 2})
	stamp := statsTimestamp(t, p, snapshot, nil)
	writer, retire := statsReportingConn(t, p, b)
	statsExec(t, p, b, writer, "BEGIN", 0)
	statsExec(t, p, b, writer, "ANALYZE "+name, 0)
	reported := statsReportingVector{2, 0, 0, 2, 0, 0, 2}
	statsMaintenanceWant(t, p, b, observer, name, 1, reported)
	statsMaintenanceWant(t, p, b, snapshot, name, 0, statsReportingVector{2, 0, 0, 2, 0, 2, 2})
	if got := statsTimestamp(t, p, snapshot, nil); !got.Valid || !stamp.Valid || !got.Time.Equal(stamp.Time) {
		t.Fatal("maintenance changed held smoke snapshot")
	}
	statsClear(t, p, b, snapshot)
	statsNoTimestamp(t, p, snapshot, nil)
	statsMaintenanceWant(t, p, b, snapshot, name, 1, reported)
	statsExec(t, p, b, writer, "ROLLBACK", 0)
	retire()
	statsMaintenanceWant(t, p, b, observer, name, 1, reported)
	statsExec(t, p, b, snapshot, "ROLLBACK", 0)
	retireSnapshot()
}

func statsMaintenanceColumnPartition(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, prepared bool) {
	t.Helper()
	name := fmt.Sprintf("stats_maintenance_column_%t", prepared)
	statsCreate(t, p, b, observer, name)
	statsPublish(t, p, b, name, 2, fixture.Exec("INSERT INTO "+name+" VALUES(1),(2)", 2))
	writer, retire := statsReportingConn(t, p, b)
	statsReportingExec(t, p, b, writer, prepared, "BEGIN", 0)
	statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name+"(id)", 0)
	statsMaintenanceWant(t, p, b, observer, name, 1, statsReportingVector{2, 0, 0, 2, 0, 2, 2})
	statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
	statsMaintenanceWant(t, p, b, observer, name, 2, statsReportingVector{2, 0, 0, 2, 0, 0, 2})
	statsReportingExec(t, p, b, writer, prepared, "ROLLBACK", 0)
	retire()
	parent := fmt.Sprintf("stats_maintenance_parent_%t", prepared)
	child := parent + "_leaf"
	statsExec(t, p, b, observer, "CREATE TABLE "+parent+"(id integer) PARTITION BY RANGE(id)", 0)
	statsExec(t, p, b, observer, "CREATE TABLE "+child+" PARTITION OF "+parent+" FOR VALUES FROM(0) TO(10)", 0)
	statsPublish(t, p, b, child, 2, fixture.Exec("INSERT INTO "+parent+" VALUES(1),(2)", 2))
	writer, retire = statsReportingConn(t, p, b)
	statsReportingExec(t, p, b, writer, prepared, "BEGIN", 0)
	statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+parent, 0)
	statsMaintenanceWant(t, p, b, observer, parent, 1, statsReportingVector{})
	statsReportingExec(t, p, b, writer, prepared, "ROLLBACK", 0)
	retire()
	statsMaintenanceWant(t, p, b, observer, parent, 1, statsReportingVector{})
}

func statsMaintenanceRecreate(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, prepared bool) {
	t.Helper()
	name := fmt.Sprintf("stats_maintenance_recreate_%t", prepared)
	writer, retire := statsReportingConn(t, p, b)
	statsExec(t, p, b, writer, "SET stats_fetch_consistency=none", 0)
	statsReportingExec(t, p, b, writer, prepared, "BEGIN", 0)
	for _, q := range []string{"CREATE TABLE " + name + "(id integer)", "INSERT INTO " + name + " VALUES(1)", "ANALYZE " + name} {
		n := int64(0)
		if q == "INSERT INTO "+name+" VALUES(1)" {
			n = 1
		}
		statsReportingExec(t, p, b, writer, prepared, q, n)
	}
	statsMaintenanceWant(t, p, b, writer, name, 1, statsReportingVector{})
	var oldOID, newOID int64
	query := "SELECT oid::bigint FROM pg_class WHERE relname='" + name + "'"
	if err := writer.QueryRowContext(p.Ctx, query).Scan(&oldOID); err != nil {
		t.Fatal(err)
	}
	statsReportingExec(t, p, b, writer, prepared, "DROP TABLE "+name, 0)
	statsReportingExec(t, p, b, writer, prepared, "CREATE TABLE "+name+"(id integer)", 0)
	statsReportingExec(t, p, b, writer, prepared, "INSERT INTO "+name+" VALUES(7)", 1)
	statsReportingExec(t, p, b, writer, prepared, "ANALYZE "+name, 0)
	if err := writer.QueryRowContext(p.Ctx, query).Scan(&newOID); err != nil {
		t.Fatal(err)
	}
	if oldOID == newOID {
		t.Fatal("drop/recreate reused qualified object identity")
	}
	statsMaintenanceWant(t, p, b, writer, name, 1, statsReportingVector{})
	statsReportingExec(t, p, b, writer, prepared, "COMMIT", 0)
	want := statsReportingVector{1, 0, 0, 1, 0, 1, 1}
	statsReportingDone(t, p, b, observer, retire, name, want, []int64{7})
	statsMaintenanceWant(t, p, b, observer, name, 1, want)
}
