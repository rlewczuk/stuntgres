package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Relation NOWAIT admission independently witnesses transaction ownership;
// session advisory probes witness the separate physical-session lifetime.
// A savepoint failure releases later relation holds, not earlier/session holds.
func datetimeSleepOwners(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	run("SET statement_timeout=0")
	run("CREATE TABLE sleep_owner_earlier(id integer)")
	run("CREATE TABLE sleep_owner_later(id integer)")
	relationGrant := func(t *testing.T, table string, want bool) {
		t.Helper()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
		defer func() {
			if _, err := peer.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
				t.Error("rolling back peer admission probe", err)
			}
		}()
		q := "LOCK TABLE " + table + " IN ACCESS EXCLUSIVE MODE NOWAIT"
		if want {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(q, 0))
		} else {
			_, err := peer.ExecContext(p.Ctx, q)
			diagnostic := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+table+`"`)
			fixture.State(t, err, diagnostic.Code)
			fixture.AssertDiagnostic(t, err, &diagnostic)
		}
	}
	lock := func(q string) {
		var v any
		if err := c.QueryRowContext(p.Ctx, q).Scan(&v); err != nil {
			t.Fatal(err)
		}
	}
	probe := func(name string, wantEarlier, wantLater, wantSession bool) {
		t.Run(name, func(t *testing.T) {
			relationGrant(t, "sleep_owner_earlier", wantEarlier)
			relationGrant(t, "sleep_owner_later", wantLater)
			var session bool
			if err := peer.QueryRowContext(p.Ctx, "SELECT pg_try_advisory_lock(918003)").Scan(&session); err != nil {
				t.Fatal(err)
			}
			if session != wantSession {
				t.Fatalf("ownership boundary=%s session=%t want=%t", name, session, wantSession)
			}
			if session {
				var released bool
				if err := peer.QueryRowContext(p.Ctx, "SELECT pg_advisory_unlock(918003)").Scan(&released); err != nil || !released {
					t.Fatal("releasing probe session hold", released, err)
				}
			}
		})
	}
	lock("SELECT pg_advisory_lock(918003)")
	defer c.ExecContext(p.Ctx, "SELECT pg_advisory_unlock(918003)")
	run("BEGIN")
	defer c.ExecContext(p.Ctx, "ROLLBACK")
	run("LOCK TABLE sleep_owner_earlier IN ACCESS EXCLUSIVE MODE")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_probe VALUES(1)", 1))
	run("SAVEPOINT sleep_before")
	run("LOCK TABLE sleep_owner_later IN ACCESS EXCLUSIVE MODE")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_probe VALUES(2)", 1))
	datetimeSleepValue(t, p, b, c, "SELECT pg_sleep(0) AS result", false)
	probe("successful-sleep-retains-all-holds", false, false, false)
	// Only the failing sleep witnesses the short timeout, not lock/setup work.
	run("SET statement_timeout='100ms'")
	datetimeSleepFailure(t, p, b, c, "SELECT pg_sleep(10)", datetimeSleepError("57014", "canceling statement due to statement timeout"), false, false)
	probe("after-timeout-before-recovery", false, true, false)
	run("ROLLBACK TO SAVEPOINT sleep_before")
	run("SET statement_timeout=0")
	probe("after-rollback-to", false, true, false)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM sleep_cancel_probe ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	run("ROLLBACK")
	probe("after-full-rollback", true, true, false)
	fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) AS n FROM sleep_cancel_probe", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	lock("SELECT pg_advisory_unlock(918003)")
	probe("after-session-unlock", true, true, true)
}
