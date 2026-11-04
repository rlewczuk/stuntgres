package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetDatabaseLifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, self := range []bool{false, true} {
			label := mode + "_peer"
			if self {
				label = mode + "_self"
			}
			t.Run(label, func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					statsCreate(t, p, b, c, "reset_lifetime_rows")
					statsPublish(t, p, b, "reset_lifetime_rows", 5, fixture.Exec("INSERT INTO reset_lifetime_rows VALUES(1),(2),(3),(4),(5)", 5))
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset_single_function_counters(0::oid)", "pg_stat_reset_single_function_counters", "")
					activityExec(t, p, c, "SET stats_fetch_consistency="+mode)
					activityExec(t, p, c, "BEGIN")
					statsNoTimestamp(t, p, c, nil)
					statsWantCount(t, p, c, "reset_lifetime_rows", 5)
					var name string
					if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&name); err != nil {
						t.Fatal(err)
					}
					old := resetDatabaseTimestamp(t, p, c, name)
					capture := statsTimestamp(t, p, c, nil)
					resetter := peer
					if self {
						resetter = c
					}
					resetDatabaseValue(t, p, b, resetter, "SELECT pg_stat_reset()", "pg_stat_reset", "")
					want := int64(5)
					if mode == "none" {
						want = 0
					}
					statsWantCount(t, p, c, "reset_lifetime_rows", want)
					if current := resetDatabaseTimestamp(t, p, c, name); mode != "none" && !resetDatabaseSameTime(current, old) {
						t.Fatal("reset invalidated cached database timestamp", current, old)
					}
					if current := statsTimestamp(t, p, c, nil); !resetDatabaseSameTime(current, capture) {
						t.Fatal("reset changed snapshot construction time", current, capture)
					}
					statsClear(t, p, b, c)
					statsWantCount(t, p, c, "reset_lifetime_rows", 0)
					if !resetDatabaseTimestamp(t, p, c, name).Valid {
						t.Fatal("clear did not expose reset timestamp")
					}
					activityExec(t, p, c, "ROLLBACK")
					statsWantCount(t, p, c, "reset_lifetime_rows", 0)
					activityExec(t, p, c, "BEGIN")
					statsClear(t, p, b, c)
					resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset()", "pg_stat_reset", "")
					statsNoTimestamp(t, p, c, nil)
					activityExec(t, p, c, "ROLLBACK")
				})
			})
		}
	}
	for _, finish := range []string{"rollback", "savepoint", "prepared_commit", "prepared_rollback"} {
		t.Run(finish, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				statsCreate(t, p, b, c, "reset_rollback_rows")
				statsPublish(t, p, b, "reset_rollback_rows", 2, fixture.Exec("INSERT INTO reset_rollback_rows VALUES(1),(2)", 2))
				if finish == "prepared_commit" || finish == "prepared_rollback" {
					fixture.PreparedReferenceCleanup(t, b, "database_reset_public")
				}
				resetDatabaseExec(t, p, c, "SET stats_fetch_consistency=none", "BEGIN")
				if finish == "savepoint" {
					activityExec(t, p, c, "SAVEPOINT reset_mark")
				}
				resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset_single_table_counters('reset_rollback_rows'::regclass::oid)", "pg_stat_reset_single_table_counters", "")
				switch finish {
				case "savepoint":
					resetDatabaseExec(t, p, c, "ROLLBACK TO reset_mark", "ROLLBACK")
				case "prepared_commit":
					resetDatabaseExec(t, p, c, "PREPARE TRANSACTION 'database_reset_public'", "COMMIT PREPARED 'database_reset_public'")
				case "prepared_rollback":
					resetDatabaseExec(t, p, c, "PREPARE TRANSACTION 'database_reset_public'", "ROLLBACK PREPARED 'database_reset_public'")
				default:
					activityExec(t, p, c, "ROLLBACK")
				}
				statsWantCount(t, p, c, "reset_rollback_rows", 0)
				statsReportingRows(t, p, b, c, "reset_rollback_rows", []int64{1, 2})
			})
		})
	}
}
