package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceBackendStateLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		mapped := stateMapped(t, p, b, o, peer)
		id, pid := mapped[0], mapped[1].(int64)
		held := statePrepare(t, p, o)
		if stateRead(t, p, b, o, id)[2] != nil {
			t.Fatal("idle peer has transaction start")
		}
		for _, prepared := range []bool{false, true} {
			query := "/* exact backend source é */ SELECT 41"
			if prepared {
				query = "/* retained backend source */ SELECT $1::integer"
				s, err := peer.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				var n int
				err = s.QueryRowContext(p.Ctx, 73).Scan(&n)
				s.Close()
				if err != nil || n != 73 {
					t.Fatal(n, err)
				}
			} else {
				activityExec(t, p, peer, query)
			}
			view := statActivityGet(t, p, o, pid, true)
			before := stateRead(t, p, b, o, id)
			stateSource(t, before, view, query)
			stateEqual(t, stateRead(t, p, b, held, id), before)
			statActivityClear(t, p, o)
			stateEqual(t, stateRead(t, p, b, o, id), before)
		}
		activityExec(t, p, peer, "CREATE TABLE bs_lifecycle(v integer)")
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			activityExec(t, p, peer, "BEGIN")
			defer peer.ExecContext(context.Background(), "ROLLBACK")
			statActivityClear(t, p, o)
			start := stateRead(t, p, b, held, id)[2]
			if stamp, ok := start.(time.Time); !ok || stamp.IsZero() {
				t.Fatal("real top transaction start", start)
			}
			activityExec(t, p, peer, "SAVEPOINT child")
			activityExec(t, p, peer, "INSERT INTO bs_lifecycle VALUES(1)")
			_, err := peer.ExecContext(p.Ctx, "SELECT 1/0")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			activityExec(t, p, peer, "ROLLBACK TO child")
			statActivityClear(t, p, o)
			stateEqual(t, []any{stateRead(t, p, b, held, id)[2]}, []any{start})
			activityExec(t, p, peer, end)
			statActivityClear(t, p, o)
			if stateRead(t, p, b, held, id)[2] != nil {
				t.Fatal("ended top transaction time")
			}
		}
		activityExec(t, p, peer, "BEGIN")
		_, err := peer.ExecContext(p.Ctx, "SELECT 1/0")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
		statActivityClear(t, p, o)
		if stateRead(t, p, b, o, id)[2] != nil {
			t.Fatal("aborted top transaction time")
		}
		activityExec(t, p, peer, "ROLLBACK")
		activityExec(t, p, peer, "SET track_activities=off")
		activityExec(t, p, peer, "SELECT 123")
		statActivityClear(t, p, o)
		disabled := stateRead(t, p, b, held, id)
		stateEqual(t, disabled[:3], []any{"<command string not enabled>", nil, nil})
		stateIdle(t, b.PG, disabled)
		activityExec(t, p, peer, "SET track_activities=on")
		activityExec(t, p, peer, "/* tracking restored */ SELECT 124")
		statActivityClear(t, p, o)
		stateSource(t, stateRead(t, p, b, o, id), statActivityGet(t, p, o, pid, true), "/* tracking restored */ SELECT 124")
		// Go controls have no source on native; lib/pq actually sends its BEGIN.
		for _, commit := range []bool{false, true} {
			view := statActivityGet(t, p, o, pid, true)
			tx, err := peer.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			statActivityClear(t, p, o)
			row := stateRead(t, p, b, held, id)
			if b.PG {
				if row[0] != "BEGIN READ WRITE" {
					t.Fatal("actual lib/pq BEGIN", row)
				}
			} else {
				stateEqual(t, row[:2], []any{view[20], view[12]})
			}
			q := "/* tx statement */ SELECT v FROM bs_lifecycle ORDER BY v"
			r, err := tx.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for r.Next() {
				var n int
				if err := r.Scan(&n); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
			r.Close()
			last := statActivityGet(t, p, o, pid, true)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			statActivityClear(t, p, o)
			row = stateRead(t, p, b, held, id)
			if row[2] != nil {
				t.Fatal("Go Tx ended time", row)
			}
			if !b.PG {
				stateEqual(t, row[:2], []any{q, last[12]})
			}
		}
		// PID-shaped inputs are missing only when proven absent from the slot set.
		found := false
		for _, row := range identitySet(t, p, b, o) {
			found = found || row[0] == pid
		}
		if !found {
			stateAbsent(t, p, b, o, pid)
		}
		admin := statActivityConn(t, p, b)
		statActivitySignal(t, p, admin, pid, true)
		statActivityRetired(t, p, admin, pid)
		statActivityClear(t, p, o)
		identityRetiredFresh(t, p, b, o, mapped)
		for _, row := range identitySet(t, p, b, o) {
			if row[0] == id {
				stateSource(t, stateRead(t, p, b, o, id), statActivityGet(t, p, o, row[1].(int64), false), statActivityGet(t, p, o, row[1].(int64), false).text(20))
				return
			}
		}
		stateAbsent(t, p, b, o, id.(int64))
	})
}
