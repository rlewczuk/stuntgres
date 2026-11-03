package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestStatDatabaseSnapshotFirstAccess(t *testing.T) {
	for _, first := range []struct {
		name, query string
	}{
		{"index", "SELECT idx_scan FROM pg_catalog.pg_stat_all_indexes WHERE indexrelname='stat_db_snapshot_idx'"},
		{"archiver", "SELECT archived_count FROM pg_catalog.pg_stat_archiver"},
		{"bgwriter", "SELECT buffers_clean FROM pg_catalog.pg_stat_bgwriter"},
		{"slru", "SELECT blks_hit FROM pg_catalog.pg_stat_slru WHERE name='transaction'"},
	} {
		for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(first.name+"/"+terminal, func(t *testing.T) {
				e := engine.New()
				setup := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, setup,
					"CREATE DATABASE stat_db_snapshot_b",
					"CREATE DATABASE stat_db_snapshot_c",
					"CREATE TABLE stat_db_snapshot_local(id integer)",
					"CREATE INDEX stat_db_snapshot_idx ON stat_db_snapshot_local USING hash(id)")
				statsFunctionRetire(t, setup)
				left, err := e.SelectDatabase(t.Context(), "stat_db_snapshot_b")
				if err != nil {
					t.Fatal(err)
				}
				right, err := e.SelectDatabase(t.Context(), "stat_db_snapshot_c")
				if err != nil {
					t.Fatal(err)
				}
				publish := func(target *core.Engine, setup bool, query string) {
					writer := enginetest.StartSession(t, target, nil)
					if setup {
						statsFunctionExec(t, writer, "CREATE TABLE stat_db_rows(id integer)")
					}
					statsFunctionExec(t, writer, query)
					statsFunctionRetire(t, writer)
				}
				publish(left, true, "INSERT INTO stat_db_rows VALUES(1),(2)")
				publish(right, true, "INSERT INTO stat_db_rows VALUES(1),(2),(3)")
				if b, c := statDatabaseRuntimeFact(left), statDatabaseRuntimeFact(right); b.Inserts != 2 || c.Inserts != 3 || b.Commits != 2 || c.Commits != 2 || b.Sessions != 1 || c.Sessions != 1 {
					t.Fatal("independent positive published B/C setup", b, c)
				}
				observer := enginetest.StartSession(t, e, nil)
				rows := func(query string, want [][]driver.Value) {
					t.Helper()
					result := enginetest.SessionSQL(t, observer, query)
					got := enginetest.Tuples(result)
					if err := result.Rows.Close(); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s: got %v want %v", query, got, want)
					}
				}
				const peers = "SELECT tup_inserted,xact_commit,xact_rollback,sessions FROM pg_catalog.pg_stat_database WHERE datname IN ('stat_db_snapshot_b','stat_db_snapshot_c') ORDER BY datname"
				const stamp = "SELECT pg_catalog.pg_stat_get_snapshot_timestamp()"
				statsFunctionExec(t, observer, "SET stats_fetch_consistency=snapshot", "BEGIN")
				defer statsFunctionExec(t, observer, "ROLLBACK")
				rows(stamp, [][]driver.Value{{nil}})
				rows(first.query, [][]driver.Value{{int64(0)}})
				// No B/C SQL access has occurred in the retained transaction yet.
				publish(left, false, "INSERT INTO stat_db_rows VALUES(11),(12),(13),(14)")
				publish(right, false, "INSERT INTO stat_db_rows VALUES(11),(12),(13),(14),(15)")
				if b, c := statDatabaseRuntimeFact(left), statDatabaseRuntimeFact(right); b.Inserts != 6 || c.Inserts != 8 {
					t.Fatal("peer writes were not published before retained reads", b, c)
				}
				rows(peers, [][]driver.Value{{int64(2), int64(2), int64(0), int64(1)}, {int64(3), int64(2), int64(0), int64(1)}})
				rows("SELECT CASE WHEN pg_catalog.pg_stat_get_snapshot_timestamp() IS NOT NULL THEN 1 ELSE 0 END", [][]driver.Value{{int64(1)}})
				resetStatsExec(t, observer, "SELECT pg_catalog.pg_stat_clear_snapshot()")
				rows(stamp, [][]driver.Value{{nil}})
				rows(first.query, [][]driver.Value{{int64(0)}})
				rows(peers, [][]driver.Value{{int64(6), int64(3), int64(0), int64(2)}, {int64(8), int64(3), int64(0), int64(2)}})
				publish(left, false, "INSERT INTO stat_db_rows VALUES(21),(22),(23),(24),(25),(26)")
				publish(right, false, "INSERT INTO stat_db_rows VALUES(21),(22),(23),(24),(25),(26),(27)")
				if b, c := statDatabaseRuntimeFact(left), statDatabaseRuntimeFact(right); b.Inserts != 12 || c.Inserts != 15 {
					t.Fatal("terminal refresh lacks independently published peers", b, c)
				}
				rows(peers, [][]driver.Value{{int64(6), int64(3), int64(0), int64(2)}, {int64(8), int64(3), int64(0), int64(2)}})
				statsFunctionExec(t, observer, terminal, "BEGIN")
				rows(stamp, [][]driver.Value{{nil}})
				rows(first.query, [][]driver.Value{{int64(0)}})
				rows(peers, [][]driver.Value{{int64(12), int64(4), int64(0), int64(3)}, {int64(15), int64(4), int64(0), int64(3)}})
			})
		}
	}
}
