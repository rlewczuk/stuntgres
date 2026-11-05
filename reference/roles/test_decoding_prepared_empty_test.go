package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// These are empty surviving projections, not read-only transactions: a genuine
// child INSERT is captured and then aborted before PREPARE. Buffered decoding
// isolates empty-transaction callbacks; immediate decoding also witnesses the
// live child's actual streamed block before its rollback.
func TestReferenceTestDecodingRollbackOnlyPrepared(t *testing.T) {
	for _, twoPhase := range []bool{false, true} {
		for _, mode := range []string{"off", "buffered", "immediate"} {
			for _, skipEmpty := range []bool{false, true} {
				if mode == "immediate" && !skipEmpty {
					continue
				}
				for _, commit := range []bool{false, true} {
					t.Run(fmt.Sprintf("two_phase=%v/stream=%s/skip_empty=%v/commit=%v", twoPhase, mode, skipEmpty, commit), func(t *testing.T) {
						tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
							tdTable(s)
							s.Exec("CREATE TABLE td_barrier(v integer)")
							streamMode := "buffered"
							if mode == "immediate" {
								streamMode = mode
							}
							s.Exec("SET debug_logical_replication_streaming=" + streamMode)
							s.Exec("SET logical_decoding_work_mem='64MB'")
							var name, lsn string
							if err := s.C.QueryRowContext(s.P.Ctx, "SELECT * FROM pg_create_logical_replication_slot('td_slot','test_decoding',false,$1)", twoPhase).Scan(&name, &lsn); err != nil || name != "td_slot" || lsn == "" {
								s.T.Fatal("create rollback-only slot", name, lsn, err)
							}
							writer := tdPeer(s, db)
							s.T.Cleanup(func() {
								ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
								defer cancel()
								var exists bool
								if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_prepared_xacts WHERE gid='td_rollback_only')").Scan(&exists); err != nil {
									s.T.Error(err)
								} else if exists {
									if _, err := db.ExecContext(ctx, "ROLLBACK PREPARED 'td_rollback_only'"); err != nil {
										s.T.Error(err)
									}
								}
							})
							streamOption, emptyOption := "0", "0"
							if mode != "off" {
								streamOption = "1"
							}
							if skipEmpty {
								emptyOption = "1"
							}
							options := ",'stream-changes','" + streamOption + "','skip-empty-xacts','" + emptyOption + "'"
							tdExec(s, writer, "BEGIN", 0)
							xid := tdXID(s, writer)
							tdExec(s, writer, "SAVEPOINT discarded", 0)
							tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
							barrierXID := tdBarrier(s, db)
							read := func(fn string) []tdRow {
								rows := tdRead(s, fn, nil, options)
								for _, row := range rows {
									if row.XID != xid && row.XID != barrierXID {
										s.T.Fatal("unexpected transaction in rollback-only history", row, xid, barrierXID)
									}
								}
								return tdForXID(rows, xid)
							}
							var activeRows []string
							if mode == "immediate" {
								activeRows = []string{tdOpen, tdChange, tdClose}
							}
							tdAssertRows(s.T, read("pg_logical_slot_peek_changes"), activeRows...)
							predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, nil)
							tdExec(s, writer, "ROLLBACK TO discarded", 0)
							tdExec(s, writer, "PREPARE TRANSACTION 'td_rollback_only'", 0)
							predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, nil)
							predefinedSelect(s, "SELECT gid FROM pg_prepared_xacts WHERE gid='td_rollback_only'", []string{"gid"}, []string{"TEXT"}, [][]any{{"td_rollback_only"}})
							var prepareRows []string
							if twoPhase && !skipEmpty {
								prepareRows = []string{"BEGIN", "PREPARE TRANSACTION 'td_rollback_only'"}
							}
							before := tdPosition(s)
							held := read("pg_logical_slot_peek_changes")
							tdAssertRows(s.T, held, prepareRows...)
							again := read("pg_logical_slot_peek_changes")
							if !reflect.DeepEqual(held, again) || tdPosition(s) != before {
								s.T.Fatal("prepare peek changed rows or confirmation", held, again)
							}
							tdAssertRows(s.T, read("pg_logical_slot_get_changes"), prepareRows...)
							tdAssertRows(s.T, read("pg_logical_slot_get_changes"))
							finish := "ROLLBACK PREPARED"
							if commit {
								finish = "COMMIT PREPARED"
							}
							tdExec(s, writer, finish+" 'td_rollback_only'", 0)
							var finishRows []string
							if twoPhase {
								finishRows = []string{finish + " 'td_rollback_only'"}
							} else if commit && !skipEmpty {
								finishRows = []string{"BEGIN", "COMMIT"}
							}
							tdAssertRows(s.T, read("pg_logical_slot_get_changes"), finishRows...)
							tdAssertRows(s.T, held, prepareRows...)
							tdGet(s, options)
							predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, nil)
							predefinedSelect(s, "SELECT gid FROM pg_prepared_xacts WHERE gid='td_rollback_only'", []string{"gid"}, []string{"TEXT"}, nil)
							// A later real row must remain decodable after either finish,
							// including when every earlier get returned no visible rows.
							s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
							laterRows := tdTxn(tdInsert(2))
							if mode == "immediate" {
								laterRows = []string{tdOpen, tdChange, tdClose, tdCommit}
							}
							tdGet(s, options, laterRows...)
							tdGet(s, options)
						})
					})
				}
			}
		}
	}
}
