package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func resetDatabaseTimestamp(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) sql.NullTime {
	t.Helper()
	var stamp sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_stat_database WHERE datname=$1", name).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func resetDatabaseSameTime(a, b sql.NullTime) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Time.Equal(b.Time))
}
func TestReferenceStatsResetDatabaseCounters(t *testing.T) {
	statDatabaseScenario(t, "reset_counters", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		target, peer := create("target"), create("peer")
		base := statDatabaseSetup(t, p, target, observer)
		peerBase := statDatabaseSetup(t, p, peer, observer)
		writer, retire := statDatabaseConn(t, p, target)
		// ReportingConn already owns failure cleanup; retire exactly once below.
		statDatabaseWriterBaseline(t, p, target, observer, writer, base, 0)
		activityExec(t, p, writer, "BEGIN")
		before := statDatabaseGet(t, p, observer, target.Name)
		stamp := resetDatabaseTimestamp(t, p, observer, target.Name)
		for _, name := range []string{"pg_stat_reset_single_table_counters", "pg_stat_reset_single_function_counters"} {
			resetDatabaseValue(t, p, target, writer, "SELECT "+name+"(NULL::oid)", name, nil)
			current, counters := resetDatabaseTimestamp(t, p, observer, target.Name), statDatabaseGet(t, p, observer, target.Name)
			if !resetDatabaseSameTime(current, stamp) || counters != before {
				t.Fatalf("strict NULL changed published state: timestamp got=%+v want=%+v counters got=%+v want=%+v", current, stamp, counters, before)
			}
			for _, oid := range []string{"0", "4294967295", "2147483000"} {
				resetDatabaseValue(t, p, target, writer, "SELECT "+name+"("+oid+"::oid)", name, "")
				current := resetDatabaseTimestamp(t, p, observer, target.Name)
				if !current.Valid {
					t.Fatal("absent OID failed timestamp")
				}
				stamp = current
				if statDatabaseGet(t, p, observer, target.Name) != before {
					t.Fatal("absent OID changed counters")
				}
			}
		}
		var sharedBefore sql.NullTime
		if err := observer.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_stat_database WHERE datid=0").Scan(&sharedBefore); err != nil {
			t.Fatal(err)
		}
		resetDatabaseValue(t, p, target, writer, "SELECT pg_stat_reset()", "pg_stat_reset", "")
		got := statDatabaseGet(t, p, observer, target.Name)
		if got != (statDatabaseVector{Backends: 1}) {
			t.Fatal("immediate published database reset", got)
		}
		after := resetDatabaseTimestamp(t, p, observer, target.Name)
		if !after.Valid || stamp.Valid && after.Time.Before(stamp.Time) {
			t.Fatal("reset timestamp", stamp, after)
		}
		if statDatabaseGet(t, p, observer, peer.Name) != peerBase {
			t.Fatal("reset changed peer database")
		}
		var sharedAfter sql.NullTime
		if err := observer.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_stat_database WHERE datid=0").Scan(&sharedAfter); err != nil || !resetDatabaseSameTime(sharedAfter, sharedBefore) {
			t.Fatal("database reset changed shared timestamp", sharedAfter, sharedBefore, err)
		}
		// Every other cumulative database field is zero at the same published cut;
		// checksums disabled remain NULL rather than becoming numeric zero.
		var checksums string
		if err := writer.QueryRowContext(p.Ctx, "SHOW data_checksums").Scan(&checksums); err != nil {
			t.Fatal(err)
		}
		var checksum any
		if checksums == "on" {
			checksum = int64(0)
		}
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT blks_read,blks_hit,tup_returned,tup_fetched,conflicts,temp_files,temp_bytes,deadlocks,checksum_failures,checksum_last_failure,blk_read_time,blk_write_time,session_time,active_time,idle_in_transaction_time FROM pg_stat_database WHERE datname=$1", []string{"blks_read", "blks_hit", "tup_returned", "tup_fetched", "conflicts", "temp_files", "temp_bytes", "deadlocks", "checksum_failures", "checksum_last_failure", "blk_read_time", "blk_write_time", "session_time", "active_time", "idle_in_transaction_time"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), checksum, nil, float64(0), float64(0), float64(0), float64(0), float64(0)}}, target.Name))
		activityExec(t, p, writer, "ROLLBACK")
		statsExec(t, p, target, writer, "INSERT INTO stat_db_rows VALUES(1),(2)", 2)
		retire()
		statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Insert == 2 && v.Rollback == 1 && v.Backends == 0 })
		if current := resetDatabaseTimestamp(t, p, observer, target.Name); !resetDatabaseSameTime(current, after) {
			t.Fatal("rollback undid reset timestamp", current, after)
		}
	})
}
