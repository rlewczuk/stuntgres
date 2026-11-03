package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivityMetadata(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		activityExec(t, p, peer, "SET application_name='activity metadata'")
		var database, user string
		var datid, uid int64
		if err := peer.QueryRowContext(p.Ctx, "SELECT current_database(), session_user").Scan(&database, &user); err != nil {
			t.Fatal(err)
		}
		if err := o.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=$1", database).Scan(&datid); err != nil {
			t.Fatal(err)
		}
		if err := o.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", user).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		held, err := o.PrepareContext(p.Ctx, statActivitySelect)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = held.QueryContext(p.Ctx, pid)
			} else {
				rows, err = o.QueryContext(p.Ctx, statActivitySelect, pid)
			}
			if err != nil {
				t.Fatal(err)
			}
			statActivityMetadata(t, rows, b.PG)
			r, ok := statActivityRead(t, rows)
			if !ok {
				t.Fatal("missing peer")
			}
			if r.text(0) != fmt.Sprint(datid) || r.text(1) != database || r.text(2) != fmt.Sprint(pid) || r.text(4) != fmt.Sprint(uid) || r.text(5) != user || r.text(6) != "activity metadata" || r.text(21) != "client backend" {
				t.Fatal("identity", r)
			}
			if r[3] != nil {
				t.Fatal("ordinary backend has leader", r[3])
			}
			if !b.PG {
				for _, i := range []int{7, 8, 9} {
					if r[i] != nil {
						t.Fatal("invented native endpoint", r)
					}
				}
			}
			if r[10] == nil || r[12] == nil || r[13] == nil {
				t.Fatal("missing lifecycle clocks", r)
			}
		}
		rows, err := held.QueryContext(p.Ctx, int64(-1))
		if err != nil {
			t.Fatal(err)
		}
		statActivityMetadata(t, rows, b.PG)
		if _, ok := statActivityRead(t, rows); ok {
			t.Fatal("negative pid exists")
		}
		rows, err = o.QueryContext(p.Ctx, "SELECT * FROM pg_stat_activity WHERE pid=$1", pid)
		if err != nil {
			t.Fatal(err)
		}
		// Rows owns captured values rather than lazy provider/session evaluation.
		activityExec(t, p, peer, "SET application_name='after capture'")
		r, ok := statActivityRead(t, rows)
		if !ok || r.text(6) != "activity metadata" {
			t.Fatal("rows changed after capture", r)
		}
	})
}

func TestReferencePGStatActivityLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		holder := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		hpid := statActivityPID(t, p, holder)
		activityExec(t, p, o, "CREATE TABLE activity_life(v integer)")
		activityExec(t, p, peer, "SELECT 17 /* original idle */")
		first := statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, first, "idle", "SELECT 17 /* original idle */")
		if first[11] != nil {
			t.Fatal("idle transaction start", first)
		}
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		began := statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, began, "idle in transaction", "BEGIN")
		if began[11] == nil || began.stamp(t, 11).After(began.stamp(t, 12)) {
			t.Fatal("transaction start", began)
		}
		activityExec(t, p, holder, "BEGIN")
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, holder, "LOCK TABLE activity_life IN ACCESS EXCLUSIVE MODE")
		q := "LOCK TABLE activity_life IN ACCESS EXCLUSIVE MODE /* live original */"
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			_, err := peer.ExecContext(ctx, q)
			return fixture.RelationOutcome{Err: err}
		})
		joined := false
		defer func() {
			cancel()
			if !joined {
				await()
			}
		}()
		statActivityBlocker(t, p, o, pid, hpid)
		active := statActivityAwait(t, p, o, pid, "relation")
		if active.text(16) != "active" || active.text(14) != "Lock" || active.text(20) != q || active[3] != nil {
			t.Fatal("blocked activity", active)
		}
		if !active.stamp(t, 10).Equal(first.stamp(t, 10)) || active.stamp(t, 10).After(active.stamp(t, 12)) || active.stamp(t, 11).After(active.stamp(t, 12)) {
			t.Fatal("clock relationships", first, active)
		}
		activityExec(t, p, holder, "ROLLBACK")
		out := await()
		joined = true
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		idle := statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, idle, "idle in transaction", q)
		if !idle.stamp(t, 12).Equal(active.stamp(t, 12)) || idle.stamp(t, 13).Before(active.stamp(t, 13)) {
			t.Fatal("query/state clock retention")
		}
		_, err := peer.ExecContext(p.Ctx, "SELECT 1/0")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
		aborted := statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, aborted, "idle in transaction (aborted)", "SELECT 1/0")
		if aborted[11] != nil {
			t.Fatal("aborted xact_start", aborted)
		}
		activityExec(t, p, peer, "ROLLBACK")
		statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", "ROLLBACK")
		for _, q := range []string{"SET application_name='permanent'", "BEGIN", "SET LOCAL application_name='local'", "ROLLBACK", "RESET application_name"} {
			activityExec(t, p, peer, q)
			r := statActivityGet(t, p, o, pid, true)
			want := "permanent"
			if q == "SET LOCAL application_name='local'" {
				want = "local"
			}
			if q == "RESET application_name" {
				want = ""
			}
			if r.text(6) != want {
				t.Fatalf("%s app %q want %q", q, r.text(6), want)
			}
		}
		// A routine's internal commands never replace its frontend source.
		activityExec(t, p, peer, "CREATE FUNCTION activity_nested() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 3; SELECT 7'")
		activityExec(t, p, peer, "SELECT activity_nested() /* outer */")
		statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", "SELECT activity_nested() /* outer */")
		activityExec(t, p, peer, "SELECT 1; SELECT 2 /* retained message */")
		statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", "SELECT 1; SELECT 2 /* retained message */")
		// Parse failures and actual retained Prepare/Execute retain frontend text too.
		for _, prepare := range []bool{false, true} {
			bad := "SELEC 1 /* monitored parse failure */"
			var failure error
			if prepare {
				_, failure = peer.PrepareContext(p.Ctx, bad)
			} else {
				_, failure = peer.ExecContext(p.Ctx, bad)
			}
			fixture.AssertDiagnostic(t, failure, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "SELEC"`, Position: "1"})
			statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", bad)
		}
		preparedSQL := "SELECT v FROM activity_life WHERE v=$1 /* monitored prepared */"
		statement, err := peer.PrepareContext(p.Ctx, preparedSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		activityExec(t, p, holder, "BEGIN")
		activityExec(t, p, holder, "LOCK TABLE activity_life IN ACCESS EXCLUSIVE MODE")
		preparedCtx, preparedCancel := context.WithCancel(p.Ctx)
		defer preparedCancel()
		preparedAwait := fixture.LockOperation(t, preparedCtx, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := statement.QueryContext(ctx, 37)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		preparedJoined := false
		defer func() {
			preparedCancel()
			if !preparedJoined {
				out := preparedAwait()
				if out.Rows != nil {
					out.Rows.Close()
				}
			}
		}()
		statActivityBlocker(t, p, o, pid, hpid)
		monitored := statActivityAwait(t, p, o, pid, "relation")
		if monitored.text(20) != preparedSQL || monitored.text(16) != "active" {
			t.Fatal("prepared substituted source", monitored)
		}
		activityExec(t, p, holder, "ROLLBACK")
		preparedOut := preparedAwait()
		preparedJoined = true
		if preparedOut.Err != nil {
			t.Fatal(preparedOut.Err)
		}
		if preparedOut.Rows != nil {
			preparedOut.Rows.Close()
		}
		statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", preparedSQL)
		// Source-less native controls are real state changes, not fictional SQL.
		activityExec(t, p, peer, "SELECT 91 /* before driver control */")
		for _, withSQL := range []bool{false, true} {
			for _, commit := range []bool{true, false} {
				before := statActivityGet(t, p, o, pid, true)
				tx, err := peer.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback() // Release database/sql's pinned Conn even after a fatal assertion.
				during := statActivityGet(t, p, o, pid, true)
				if during.text(16) != "idle in transaction" || during[11] == nil {
					t.Fatal("driver begin", during)
				}
				if !b.PG && (during.text(20) != before.text(20) || during.text(19) != before.text(19) || !during.stamp(t, 12).Equal(before.stamp(t, 12))) {
					t.Fatal("native control invented source", before, during)
				}
				if b.PG && during.text(20) != "BEGIN READ WRITE" {
					t.Fatal("server BEGIN source", during)
				}
				latest := before
				if withSQL {
					const actualSQL = "SELECT count(*) FROM activity_life /* through driver transaction */"
					var answer int
					if err := tx.QueryRowContext(p.Ctx, actualSQL).Scan(&answer); err != nil || answer != 0 {
						t.Fatal("actual driver transaction query", answer, err)
					}
					latest = statActivityGet(t, p, o, pid, true)
					statActivityState(t, b.PG, latest, "idle in transaction", actualSQL)
					if latest[19] == nil || latest.text(19) == "0" || !latest.stamp(t, 11).Equal(during.stamp(t, 11)) {
						t.Fatal("actual driver transaction source/start/identity", during, latest)
					}
				}
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				after := statActivityGet(t, p, o, pid, true)
				if after.text(16) != "idle" || after[11] != nil {
					t.Fatal("driver end", after)
				}
				if !b.PG && (after.text(20) != latest.text(20) || after.text(19) != latest.text(19) || !after.stamp(t, 12).Equal(latest.stamp(t, 12))) {
					t.Fatal("native terminal did not retain last actual SQL", latest, after)
				}
				if b.PG {
					want := "ROLLBACK"
					if commit {
						want = "COMMIT"
					}
					if after.text(20) != want {
						t.Fatal("server terminal source", after)
					}
				}
			}
		}
		// Borrower return does not terminate the physical backend or remint birth.
		old := statActivityGet(t, p, o, pid, true)
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		peer = statActivityConn(t, p, b)
		if got := statActivityPID(t, p, peer); got != pid {
			t.Fatalf("pool reuse pid %d want %d", got, pid)
		}
		reused := statActivityGet(t, p, o, pid, true)
		if !reused.stamp(t, 10).Equal(old.stamp(t, 10)) {
			t.Fatal("pool reminted backend birth")
		}
		if err := peer.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		peer.Close()
		replacement := statActivityConn(t, p, b)
		newpid := statActivityPID(t, p, replacement)
		fresh := statActivityGet(t, p, o, newpid, true)
		if newpid == pid || fresh.stamp(t, 10).Before(old.stamp(t, 10)) {
			t.Fatal("physical replacement identity/birth", old, fresh)
		}
	})
}
