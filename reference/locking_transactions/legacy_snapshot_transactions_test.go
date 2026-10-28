package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotStatus(t *testing.T) {
	for _, tc := range []struct {
		id   int64
		want any
	}{{0, nil}, {1, "committed"}, {2, "committed"}, {4294967296, nil}, {4294967297, "committed"}, {4294967298, "committed"}, {-4294967296, nil}, {-4294967295, "committed"}, {-4294967294, "committed"}} {
		t.Run(strconv.FormatInt(tc.id, 10), func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				for _, q := range []string{"SELECT txid_status(" + strconv.FormatInt(tc.id, 10) + ") AS s", "SELECT txid_status($1) AS s"} {
					var args []any
					if q == "SELECT txid_status($1) AS s" {
						args = []any{tc.id}
					}
					identityBoth(t, p, b, c, legacySnapshotStep(b, q, []string{"s"}, []string{"TEXT"}, [][]any{{tc.want}}, args...))
				}
				q := "SELECT txid_status($1) AS s"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, id := range []any{tc.id, nil, tc.id} {
					want := tc.want
					if id == nil {
						want = nil
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"s"}, []string{"TEXT"}, [][]any{{want}}, id))
				}
			})
		})
	}
	for _, id := range []int64{-1, 9223372036854775807, -9223372036854775805} {
		t.Run("future-"+strconv.FormatInt(id, 10), func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "transaction ID " + strconv.FormatUint(uint64(id), 10) + " is in the future"}
				identityBoth(t, p, b, c, legacySnapshotError("SELECT txid_status("+strconv.FormatInt(id, 10)+")", want))
				q := "SELECT txid_status($1)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, legacySnapshotError(q, want, id))
			})
		})
	}
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(finish, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, o := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				var id int64
				if err := a.QueryRowContext(p.Ctx, "SELECT txid_current()").Scan(&id); err != nil {
					t.Fatal(err)
				}
				identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_status($1) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"in progress"}}, id))
				fixture.LockExec(t, p.Ctx, a, finish, 0)
				want := "committed"
				if finish == "ROLLBACK" {
					want = "aborted"
				}
				identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_status($1) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{want}}, id))
			})
		})
	}
}

func TestReferenceLegacySnapshotAssignment(t *testing.T) {
	for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
		t.Run(begin, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				stmt, err := c.PrepareContext(p.Ctx, "SELECT txid_current() AS id")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var previous int64
				for range 2 {
					fixture.LockExec(t, p.Ctx, c, begin, 0)
					identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS id", []string{"id"}, []string{"INT8"}, [][]any{{nil}}))
					var snap string
					var status string
					if err := c.QueryRowContext(p.Ctx, "SELECT txid_current_snapshot()::text,txid_status(1)").Scan(&snap, &status); err != nil || status != "committed" {
						t.Fatalf("nonassigning inquiries %s/%s: %v", snap, status, err)
					}
					identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS id", []string{"id"}, []string{"INT8"}, [][]any{{nil}}))
					var id int64
					if err := stmt.QueryRowContext(p.Ctx).Scan(&id); err != nil {
						t.Fatal(err)
					}
					if uint32(id) < 3 || id <= previous {
						t.Fatalf("new top identity %d after %d", id, previous)
					}
					previous = id
					identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current() AS id", []string{"id"}, []string{"INT8"}, [][]any{{id}}))
					fixture.LockExec(t, p.Ctx, c, "SAVEPOINT s", 0)
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK TO s", 0)
					fixture.LockExec(t, p.Ctx, c, "RELEASE s", 0)
					var modern string
					var assigned sql.NullInt64
					if err := c.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text,txid_current_if_assigned()").Scan(&modern, &assigned); err != nil || !assigned.Valid || assigned.Int64 != id || modern != strconv.FormatUint(uint64(id), 10) {
						t.Fatalf("shared identity %s/%v want %d: %v", modern, assigned, id, err)
					}
					fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
				}
			})
		})
	}
}

func TestReferenceLegacySnapshotCurrent(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(finish, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, o := fixture.LockConnections(t, p.Ctx, b.DB)
				bumper := snapshotConn(t, p, b)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				id := identityID(t, p, a)
				snapshotBump(t, p, bumper, id)
				var legacy, modern string
				if err := o.QueryRowContext(p.Ctx, "SELECT txid_current_snapshot()::text,pg_current_snapshot()::text").Scan(&legacy, &modern); err != nil || legacy != modern {
					t.Fatalf("same statement snapshot %s/%s: %v", legacy, modern, err)
				}
				snapshotPeerPresent(t, legacy, id, true)
				fixture.LockExec(t, p.Ctx, a, finish, 0)
				identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, int64(snapshotNumber(t, id)), legacy))
				identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,txid_current_snapshot()) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, int64(snapshotNumber(t, id))))
			})
		})
	}
}
