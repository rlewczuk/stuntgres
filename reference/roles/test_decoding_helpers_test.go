package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 contrib/test_decoding/test_decoding.c. Dynamic
// XIDs/LSNs are observed independently, never copied between the two backends.
func tdCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"td_reader"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					slotCleanup(t, backend.DB, "td_slot", "td_other", "td_phase")
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						c.ExecContext(ctx, "ROLLBACK")
						c.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
						c.ExecContext(ctx, "RESET ALL")
						c.Close()
					})
					s := &fixture.RoleMembershipScenario{T: t, P: p, Backend: backend, C: c, Record: backend.Recorder, Prepared: prepared}
					s.Exec("SET TIME ZONE 'UTC'")
					body(s, backend.DB)
				})
			}
		})
	}
}

func tdSlot(s *fixture.RoleMembershipScenario, name string) string {
	s.T.Helper()
	var got, lsn string
	q := "SELECT * FROM pg_create_logical_replication_slot($1::name,'test_decoding')"
	if err := s.C.QueryRowContext(s.P.Ctx, q, name).Scan(&got, &lsn); err != nil || got != name || lsn == "" {
		s.T.Fatal(q, got, lsn, err)
	}
	return lsn
}

func tdData(s *fixture.RoleMembershipScenario, fn, slot, extra string, want ...string) {
	s.T.Helper()
	rows := make([][]any, len(want))
	for i, v := range want {
		rows[i] = []any{v}
	}
	q := "SELECT data FROM " + fn + "('" + slot + "',NULL,NULL,'include-xids','0','skip-empty-xacts','1'" + extra + ")"
	predefinedSelect(s, q, []string{"data"}, []string{"TEXT"}, rows)
}

func tdPeek(s *fixture.RoleMembershipScenario, extra string, want ...string) {
	s.T.Helper()
	tdData(s, "pg_logical_slot_peek_changes", "td_slot", extra, want...)
}

func tdGet(s *fixture.RoleMembershipScenario, extra string, want ...string) {
	s.T.Helper()
	tdData(s, "pg_logical_slot_get_changes", "td_slot", extra, want...)
}

func tdInsert(id int) string {
	return "table public.t: INSERT: id[integer]:" + strconv.Itoa(id) + " v[text]:'v'"
}
func tdTxn(changes ...string) []string {
	return append(append([]string{"BEGIN"}, changes...), "COMMIT")
}
func tdTable(s *fixture.RoleMembershipScenario) {
	s.Exec("CREATE TABLE t(id integer PRIMARY KEY,v text)")
}
func tdDML(s *fixture.RoleMembershipScenario) {
	s.Exec("BEGIN")
	s.Run(fixture.Exec("INSERT INTO t VALUES(1,'O''Brien')", 1))
	s.Run(fixture.Exec("UPDATE t SET v=NULL", 1))
	s.Run(fixture.Exec("UPDATE t SET id=2", 1))
	s.Run(fixture.Exec("DELETE FROM t", 1))
	s.Exec("TRUNCATE t")
	s.Exec("COMMIT")
}
func tdDMLRows() []string {
	return tdTxn("table public.t: INSERT: id[integer]:1 v[text]:'O''Brien'", "table public.t: UPDATE: id[integer]:1 v[text]:null", "table public.t: UPDATE: old-key: id[integer]:1 new-tuple: id[integer]:2 v[text]:null", "table public.t: DELETE: id[integer]:2", "table public.t: TRUNCATE: (no-flags)")
}

func tdPosition(s *fixture.RoleMembershipScenario) string {
	s.T.Helper()
	var v string
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name='td_slot'").Scan(&v); err != nil {
		s.T.Fatal(err)
	}
	return v
}

type tdRow struct{ LSN, XID, Data string }

func tdRead(s *fixture.RoleMembershipScenario, fn string, limit any, extra string) []tdRow {
	s.T.Helper()
	q := "SELECT lsn,xid,data FROM " + fn + "('td_slot',NULL,$1::integer,'include-xids','0','skip-empty-xacts','1'" + extra + ")"
	var e fixture.Executor = s.C
	if s.Prepared {
		stmt, err := s.C.PrepareContext(s.P.Ctx, q)
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		e = fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}
	}
	r, err := e.QueryContext(s.P.Ctx, q, limit)
	if err != nil {
		s.T.Fatalf("%s: error=%v diagnostic=%#v", q, err, fixture.ExtractDiagnostic(s.T, err))
	}
	defer r.Close()
	types, err := r.ColumnTypes()
	if err != nil {
		s.T.Fatal(err)
	}
	if len(types) != 3 {
		s.T.Fatal(types)
	}
	for i, want := range []string{"PG_LSN", "XID", "TEXT"} {
		if types[i].DatabaseTypeName() != want {
			s.T.Fatal(i, types[i])
		}
	}
	var out []tdRow
	for r.Next() {
		var row tdRow
		if err := r.Scan(&row.LSN, &row.XID, &row.Data); err != nil {
			s.T.Fatal(err)
		}
		if row.LSN == "" || row.XID == "0" || row.XID == "" {
			s.T.Fatal("missing callback identity", row)
		}
		out = append(out, row)
	}
	if err := r.Err(); err != nil {
		s.T.Fatalf("%s: partial rows=%#v error=%v diagnostic=%#v", q, out, err, fixture.ExtractDiagnostic(s.T, err))
	}
	return out
}
func tdAssertRows(t *testing.T, rows []tdRow, want ...string) {
	t.Helper()
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Data
	}
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("decoded rows\nwant: %#v\nactual (lsn,xid,data): %#v", want, rows)
	}
}
func tdPeer(s *fixture.RoleMembershipScenario, db *sql.DB) *sql.Conn {
	s.T.Helper()
	c, err := db.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.ExecContext(ctx, "ROLLBACK")
		c.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()")
		c.Close()
	})
	return c
}
func tdExec(s *fixture.RoleMembershipScenario, e fixture.Executor, q string, n int64) {
	s.T.Helper()
	fixture.RunStep(s.T, s.P.Ctx, e, s.Record, fixture.Exec(q, n))
}
func tdXID(s *fixture.RoleMembershipScenario, c *sql.Conn) string {
	s.T.Helper()
	var xid string
	if err := c.QueryRowContext(s.P.Ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		s.T.Fatal(err)
	}
	return xid
}
func tdForXID(rows []tdRow, xid string) []tdRow {
	var out []tdRow
	for _, r := range rows {
		if r.XID == xid {
			out = append(out, r)
		}
	}
	return out
}

const tdOpen = "opening a streamed block for transaction"
const tdChange = "streaming change for transaction"
const tdTruncate = "streaming truncate for transaction"
const tdClose = "closing a streamed block for transaction"
const tdCommit = "committing streamed transaction"
const tdAbort = "aborting streamed (sub)transaction"
const tdStream = ",'stream-changes','1'"

// A real synchronous committed DML record flushes the preceding writer WAL.
// The separate barrier XID is filtered using the independently observed writer.
func tdBarrier(s *fixture.RoleMembershipScenario, db *sql.DB) string {
	s.T.Helper()
	c := tdPeer(s, db)
	tdExec(s, c, "SET synchronous_commit=on", 0)
	tdExec(s, c, "BEGIN", 0)
	xid := tdXID(s, c)
	tdExec(s, c, "INSERT INTO td_barrier VALUES(1)", 1)
	tdExec(s, c, "COMMIT", 0)
	return xid
}
func tdStreamingSetup(s *fixture.RoleMembershipScenario) {
	tdTable(s)
	s.Exec("CREATE TABLE td_barrier(v integer)")
	tdSlot(s, "td_slot")
	s.Exec("SET debug_logical_replication_streaming=immediate")
}
