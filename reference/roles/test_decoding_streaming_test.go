package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTestDecodingStreamingImmediate(t *testing.T) {
	t.Run("committed_and_limits", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			tdStreamingSetup(s)
			s.Exec("BEGIN")
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
			s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
			s.Exec("COMMIT")
			tdPeek(s, tdStream, tdOpen, tdChange, tdClose, tdOpen, tdChange, tdClose, tdCommit)
			tdPeek(s, ",'stream-changes','0'", tdTxn(tdInsert(1), tdInsert(2))...)
			tdPeek(s, tdStream+",'stream-changes','0'", tdTxn(tdInsert(1), tdInsert(2))...)
			s.Exec("SET debug_logical_replication_streaming=buffered")
			s.Exec("SET logical_decoding_work_mem='64MB'")
			tdPeek(s, tdStream, tdTxn(tdInsert(1), tdInsert(2))...)
			s.Exec("SET debug_logical_replication_streaming=immediate")
			tdAssertRows(s.T, tdRead(s, "pg_logical_slot_get_changes", int64(1), tdStream), tdOpen, tdChange, tdClose)
			// Fresh decoder reconstructs the retained first row before the second
			// trigger. This exact continuation is an explicit oracle hypothesis.
			tdGet(s, tdStream, tdOpen, tdChange, tdChange, tdClose, tdCommit)
			tdGet(s, tdStream)
		})
	})
	for _, more := range []bool{false, true} {
		t.Run(fmt.Sprintf("live_more=%v", more), func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) { tdLiveContinuation(s, db, more) })
		})
	}
	t.Run("truncate_and_empty", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			tdStreamingSetup(s)
			s.Exec("TRUNCATE t")
			tdPeek(s, tdStream, tdOpen, tdTruncate, tdClose, tdCommit)
			tdGet(s, tdStream, tdOpen, tdTruncate, tdClose, tdCommit)
			s.Exec("CREATE TABLE td_empty(v integer)")
			tdPeek(s, tdStream)
			tdGet(s, tdStream+",'skip-empty-xacts','0'", "BEGIN", "COMMIT")
		})
	})
}

func tdLiveContinuation(s *fixture.RoleMembershipScenario, db *sql.DB, more bool) {
	s.T.Helper()
	tdStreamingSetup(s)
	writer := tdPeer(s, db)
	tdExec(s, writer, "BEGIN", 0)
	xid := tdXID(s, writer)
	tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
	tdBarrier(s, db)
	before := tdPosition(s)
	a := tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid)
	tdAssertRows(s.T, a, tdOpen, tdChange, tdClose)
	b := tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid)
	if !reflect.DeepEqual(a, b) || tdPosition(s) != before {
		s.T.Fatal("stable live peek/frontier", a, b, before, tdPosition(s))
	}
	tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_get_changes", nil, tdStream), xid), tdOpen, tdChange, tdClose)
	var retained bool
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT restart_lsn<= $1::pg_lsn AND confirmed_flush_lsn>=$1::pg_lsn FROM pg_replication_slots WHERE slot_name='td_slot'", a[0].LSN).Scan(&retained); err != nil || !retained {
		s.T.Fatal("open restart anchor lost", a, retained, err)
	}
	tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_get_changes", nil, tdStream), xid))
	if more {
		tdExec(s, writer, "INSERT INTO t VALUES(2,'v')", 1)
	}
	tdExec(s, writer, "COMMIT", 0)
	rows := tdForXID(tdRead(s, "pg_logical_slot_get_changes", nil, tdStream), xid)
	// Both hypotheses come from fresh SQL reorder state, not persistent slot
	// stream state. A disagreement reports every actual callback LSN and XID.
	if more {
		tdAssertRows(s.T, rows, tdOpen, tdChange, tdChange, tdClose, tdCommit)
	} else {
		tdAssertRows(s.T, rows, tdTxn(tdInsert(1))...)
	}
	tdGet(s, tdStream)
}

func tdThreshold(t *testing.T, rows []tdRow, n int, terminal bool) {
	t.Helper()
	blocks, changes, inside, commits := 0, 0, false, 0
	blockChanges := 0
	for _, row := range rows {
		switch row.Data {
		case tdOpen:
			if inside {
				t.Fatalf("nested block: %#v", rows)
			}
			inside = true
			blocks++
			blockChanges = 0
		case tdChange:
			if !inside {
				t.Fatalf("change outside block: %#v", rows)
			}
			changes++
			blockChanges++
		case tdClose:
			if !inside || blockChanges == 0 {
				t.Fatalf("empty/unopened block: %#v", rows)
			}
			inside = false
		case tdCommit:
			if inside {
				t.Fatalf("commit inside block: %#v", rows)
			}
			commits++
		default:
			t.Fatalf("nonstream output: %#v", rows)
		}
	}
	wantCommits := 0
	if terminal {
		wantCommits = 1
	}
	if inside || blocks < 1 || changes < 1 || changes > n || terminal && changes != n || commits != wantCommits || len(rows) != changes+2*blocks+wantCommits {
		t.Fatalf("stream threshold n=%d terminal=%v: %#v", n, terminal, rows)
	}
	if terminal && rows[len(rows)-1].Data != tdCommit {
		t.Fatalf("terminal not last: %#v", rows)
	}
}

func TestReferenceTestDecodingStreamingThreshold(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("live=%v", live), func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
				s.Exec("CREATE TABLE t(id integer,pad text)")
				s.Exec("CREATE TABLE td_barrier(v integer)")
				tdSlot(s, "td_slot")
				s.Exec("SET debug_logical_replication_streaming=buffered")
				s.Exec("SET logical_decoding_work_mem='64kB'")
				writer := tdPeer(s, db)
				tdExec(s, writer, "BEGIN", 0)
				xid := tdXID(s, writer)
				for i := 1; i <= 512; i++ {
					tdExec(s, writer, fmt.Sprintf("INSERT INTO t VALUES(%d,repeat('x',1000))", i), 1)
				}
				if live {
					tdBarrier(s, db)
				} else {
					tdExec(s, writer, "COMMIT", 0)
				}
				low := tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid)
				tdThreshold(s.T, low, 512, !live)
				if live {
					tdExec(s, writer, "COMMIT", 0)
				}
				s.Exec("SET logical_decoding_work_mem='64MB'")
				high := tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid)
				want := []string{"BEGIN"}
				for i := 1; i <= 512; i++ {
					want = append(want, fmt.Sprintf("table public.t: INSERT: id[integer]:%d pad[text]:'%s'", i, strings.Repeat("x", 1000)))
				}
				want = append(want, "COMMIT")
				tdAssertRows(s.T, high, want...)
			})
		})
	}
	t.Run("largest_pending_transactions", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
			s.Exec("CREATE TABLE t(id integer,pad text)")
			s.Exec("CREATE TABLE td_barrier(v integer)")
			tdSlot(s, "td_slot")
			s.Exec("SET logical_decoding_work_mem='64kB'")
			a, b := tdPeer(s, db), tdPeer(s, db)
			tdExec(s, a, "BEGIN", 0)
			xa := tdXID(s, a)
			tdExec(s, b, "BEGIN", 0)
			xb := tdXID(s, b)
			for i := 1; i <= 512; i++ {
				tdExec(s, a, fmt.Sprintf("INSERT INTO t VALUES(%d,repeat('x',1000))", i), 1)
				if i <= 8 {
					tdExec(s, b, fmt.Sprintf("INSERT INTO t VALUES(%d,'small')", 1000+i), 1)
				}
			}
			tdBarrier(s, db)
			rows := tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream)
			tdThreshold(s.T, tdForXID(rows, xa), 512, false)
			tdAssertRows(s.T, tdForXID(rows, xb))
			tdExec(s, a, "ROLLBACK", 0)
			tdExec(s, b, "ROLLBACK", 0)
		})
	})
}

func TestReferenceTestDecodingStreamingAbort(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("immediate=%v", immediate), func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
				tdStreamingSetup(s)
				writer := tdPeer(s, db)
				tdExec(s, writer, "BEGIN", 0)
				xid := tdXID(s, writer)
				tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
				tdBarrier(s, db)
				tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid), tdOpen, tdChange, tdClose)
				tdExec(s, writer, "ROLLBACK", 0)
				tdBarrier(s, db)
				if !immediate {
					s.Exec("SET debug_logical_replication_streaming=buffered")
				}
				rows := tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid)
				// A fresh decoder's catalog-live check suppresses this already-aborted
				// top transaction even in immediate mode (pinned PostgreSQL 18.6).
				tdAssertRows(s.T, rows)
				predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil)
			})
		})
	}
}

func tdWithout(rows []tdRow, excluded map[string]bool) []tdRow {
	var out []tdRow
	for _, r := range rows {
		if !excluded[r.XID] {
			out = append(out, r)
		}
	}
	return out
}

func TestReferenceTestDecodingStreamingSubtransactions(t *testing.T) {
	for _, kind := range []string{"savepoint", "nested", "release_parent", "repeated_rollback", "exception", "failed_statement"} {
		t.Run(kind, func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
				tdStreamingSetup(s)
				writer := tdPeer(s, db)
				tdExec(s, writer, "BEGIN", 0)
				top := tdXID(s, writer)
				tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
				tdExec(s, writer, "SAVEPOINT outer_save", 0)
				if kind == "nested" || kind == "release_parent" {
					tdExec(s, writer, "SAVEPOINT inner_save", 0)
				}
				tdExec(s, writer, "INSERT INTO t VALUES(2,'v')", 1)
				excluded := map[string]bool{tdBarrier(s, db): true}
				before := tdWithout(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), excluded)
				tdAssertRows(s.T, before, tdOpen, tdChange, tdClose, tdOpen, tdChange, tdClose)
				switch kind {
				case "release_parent":
					tdExec(s, writer, "RELEASE inner_save", 0)
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
				case "nested":
					tdExec(s, writer, "ROLLBACK TO inner_save", 0)
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
				case "repeated_rollback":
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
					tdExec(s, writer, "INSERT INTO t VALUES(4,'v')", 1)
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
				case "exception":
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
					tdExec(s, writer, `DO $$ BEGIN BEGIN INSERT INTO t VALUES(4,'v'); RAISE EXCEPTION 'undo'; EXCEPTION WHEN raise_exception THEN PERFORM 1; END; END $$`, 0)
				case "failed_statement":
					step := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'duplicate')", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"})
					step.ErrorStage = "execute"
					fixture.RunStep(s.T, s.P.Ctx, writer, s.Record, step)
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
				default:
					tdExec(s, writer, "ROLLBACK TO outer_save", 0)
				}
				tdExec(s, writer, "INSERT INTO t VALUES(3,'v')", 1)
				tdExec(s, writer, "COMMIT", 0)
				rows := tdWithout(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), excluded)
				// Pinned PostgreSQL 18.6 retains the attempted duplicate insertion's
				// real streamed change before the child transaction abort.
				want := []string{tdOpen, tdChange, tdClose, tdOpen, tdChange, tdClose, tdAbort}
				if kind == "failed_statement" {
					want = []string{tdOpen, tdChange, tdClose, tdOpen, tdChange, tdClose, tdOpen, tdChange, tdClose, tdAbort}
				}
				if kind == "repeated_rollback" || kind == "exception" {
					want = append(want, tdOpen, tdChange, tdClose, tdAbort)
				}
				want = append(want, tdOpen, tdChange, tdClose, tdCommit)
				tdAssertRows(s.T, rows, want...)
				for _, r := range rows {
					if r.Data == tdAbort {
						if r.XID == top {
							s.T.Fatal("child abort reused top xid", rows)
						}
					} else if r.XID != top {
						s.T.Fatal("nonabort callback lost top identity", rows)
					}
				}
				predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
			})
		})
	}
}
