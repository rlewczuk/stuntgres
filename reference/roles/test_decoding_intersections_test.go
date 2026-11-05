package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func tdTimedTerminal(t *testing.T, rows []tdRow, prefix string, lower, upper time.Time) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("missing timed terminal", prefix)
	}
	last := rows[len(rows)-1].Data
	m := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + ` \(at (\d{4}-\d\d-\d\d \d\d:\d\d:\d\d(?:\.\d{1,6})?\+00)\)$`).FindStringSubmatch(last)
	if m == nil {
		t.Fatalf("terminal timestamp grammar: %#v", rows)
	}
	stamp, err := time.Parse("2006-01-02 15:04:05.999999Z07", m[1])
	if err != nil || stamp.Before(lower) || stamp.After(upper) {
		t.Fatal("terminal timestamp bounds", lower, upper, rows, err)
	}
}

func tdClock(s *fixture.RoleMembershipScenario) time.Time {
	s.T.Helper()
	var v time.Time
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT clock_timestamp()").Scan(&v); err != nil {
		s.T.Fatal(err)
	}
	return v
}

func TestReferenceTestDecodingStreamingIntersections(t *testing.T) {
	t.Run("xids_timestamps_binary", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			tdStreamingSetup(s)
			lower := tdClock(s)
			s.Exec("BEGIN")
			xid := tdXID(s, s.C)
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
			s.Exec("TRUNCATE t")
			s.Exec("COMMIT")
			upper := tdClock(s)
			q := "SELECT lsn,xid,data FROM pg_logical_slot_peek_changes('td_slot',NULL,NULL,'include-xids','1','skip-empty-xacts','1','stream-changes','1','include-timestamp','1')"
			read := func(fn string, binary bool) []tdRow {
				query := strings.Replace(q, "pg_logical_slot_peek_changes", fn, 1)
				if binary {
					query = strings.TrimSuffix(query, ")") + ",'force-binary','1')"
				}
				r, err := s.C.QueryContext(s.P.Ctx, query)
				if err != nil {
					s.T.Fatal(err)
				}
				defer r.Close()
				types, err := r.ColumnTypes()
				if err != nil {
					s.T.Fatal(err)
				}
				wantType := "TEXT"
				if binary {
					wantType = "BYTEA"
				}
				if len(types) != 3 || types[2].DatabaseTypeName() != wantType {
					s.T.Fatal("stream result descriptors", types)
				}
				var out []tdRow
				for r.Next() {
					var row tdRow
					if err := r.Scan(&row.LSN, &row.XID, &row.Data); err != nil {
						s.T.Fatal(err)
					}
					if row.XID != xid {
						s.T.Fatal("stream top identity", row, xid)
					}
					out = append(out, row)
				}
				if err := r.Err(); err != nil {
					s.T.Fatal(err)
				}
				return out
			}
			for _, binary := range []bool{false, true} {
				fn := "pg_logical_slot_peek_changes"
				if binary {
					fn = "pg_logical_slot_peek_binary_changes"
				}
				rows := read(fn, binary)
				if len(rows) != 7 {
					s.T.Fatalf("stream intersection transcript: %#v", rows)
				}
				tdAssertRows(s.T, rows[:6], tdOpen+" TXN "+xid, "streaming change for TXN "+xid, tdClose+" TXN "+xid, tdOpen+" TXN "+xid, "streaming truncate for TXN "+xid, tdClose+" TXN "+xid)
				tdTimedTerminal(s.T, rows, tdCommit+" TXN "+xid, lower, upper)
			}
			// With empty streams enabled, TRUNCATE's catalog/invalidation records
			// also produce paired empty blocks. Their physical segment count is
			// not portable; both real visible operations and a real empty block
			// are required, in order, with one final streamed commit.
			rows := tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream+",'skip-empty-xacts','0'")
			inside, visible, empty := false, false, 0
			var operations []string
			for i, row := range rows {
				switch row.Data {
				case tdOpen:
					if inside {
						s.T.Fatal("nested empty-option block", rows)
					}
					inside, visible = true, false
				case tdChange, tdTruncate:
					if !inside {
						s.T.Fatal("operation outside block", rows)
					}
					visible = true
					operations = append(operations, row.Data)
				case tdClose:
					if !inside {
						s.T.Fatal("close without block", rows)
					}
					if !visible {
						empty++
					}
					inside = false
				case tdCommit:
					if inside || i != len(rows)-1 {
						s.T.Fatal("nonterminal commit", rows)
					}
				default:
					s.T.Fatal("unexpected stream option output", rows)
				}
			}
			if inside || empty == 0 || len(operations) != 2 || operations[0] != tdChange || operations[1] != tdTruncate || len(rows) == 0 || rows[len(rows)-1].Data != tdCommit {
				s.T.Fatal("empty block/operation contract", rows)
			}
		})
	})
	t.Run("origin_filter", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
			tdStreamingSetup(s)
			writer := tdPeer(s, db)
			var oid string
			if err := writer.QueryRowContext(s.P.Ctx, "SELECT pg_replication_origin_create('td_stream_origin')::text").Scan(&oid); err != nil {
				s.T.Fatal(err)
			}
			s.T.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				writer.ExecContext(ctx, "ROLLBACK")
				writer.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()")
				if _, err := writer.ExecContext(ctx, "SELECT pg_replication_origin_drop('td_stream_origin')"); err != nil {
					s.T.Error(err)
				}
			})
			tdExec(s, writer, "SELECT pg_replication_origin_session_setup('td_stream_origin')", 1)
			tdExec(s, writer, "BEGIN", 0)
			xid := tdXID(s, writer)
			tdExec(s, writer, "SELECT pg_replication_origin_xact_setup('0/100','2026-01-01 00:00:00+00')", 1)
			tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
			tdBarrier(s, db)
			tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid), tdOpen, tdChange, tdClose)
			tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream+",'only-local','1'"), xid))
			tdExec(s, writer, "COMMIT", 0)
			tdExec(s, writer, "SELECT pg_replication_origin_session_reset()", 1)
			s.Exec("BEGIN")
			local := tdXID(s, s.C)
			s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
			s.Exec("COMMIT")
			// Barrier is local too: independently restrict by the known tagged XID
			// for the negative, then prove a local streamed transaction survives.
			rows := tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream+",'only-local','1'")
			tdAssertRows(s.T, tdForXID(rows, xid))
			tdAssertRows(s.T, tdForXID(rows, local), tdOpen, tdChange, tdClose, tdCommit)
		})
	})
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("two_phase_commit=%v", commit), func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
				tdTable(s)
				s.T.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					var exists bool
					if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_prepared_xacts WHERE gid='td_stream_prepared')").Scan(&exists); err != nil {
						s.T.Error(err)
					} else if exists {
						if _, err := db.ExecContext(ctx, "ROLLBACK PREPARED 'td_stream_prepared'"); err != nil {
							s.T.Error(err)
						}
					}
				})
				var name, lsn string
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT * FROM pg_create_logical_replication_slot('td_slot','test_decoding',false,true)").Scan(&name, &lsn); err != nil || name != "td_slot" || lsn == "" {
					s.T.Fatal(name, lsn, err)
				}
				s.Exec("SET debug_logical_replication_streaming=immediate")
				lower := tdClock(s)
				s.Exec("BEGIN")
				s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
				s.Exec("PREPARE TRANSACTION 'td_stream_prepared'")
				upper := tdClock(s)
				rows := tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream+",'include-timestamp','1'")
				if len(rows) != 4 {
					s.T.Fatalf("stream prepare rows: %#v", rows)
				}
				tdAssertRows(s.T, rows[:3], tdOpen, tdChange, tdClose)
				tdTimedTerminal(s.T, rows, "preparing streamed transaction 'td_stream_prepared'", lower, upper)
				tdGet(s, tdStream, tdOpen, tdChange, tdClose, "preparing streamed transaction 'td_stream_prepared'")
				predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil)
				terminal := "ROLLBACK PREPARED"
				var want [][]any
				if commit {
					terminal = "COMMIT PREPARED"
					want = [][]any{{int64(1)}}
				}
				s.Exec(terminal + " 'td_stream_prepared'")
				tdGet(s, tdStream, terminal+" 'td_stream_prepared'")
				tdGet(s, tdStream)
				predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, want)
			})
		})
	}
}
