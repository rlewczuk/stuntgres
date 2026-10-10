package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceAdvisorySessionCheckConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			run := func(step referenceStep) { referenceCheckRun(t, p.ctx, a, b.recorder, step) }
			run(referenceExec("CREATE TABLE advisory_check(k bigint,CONSTRAINT advisory_check_lock CHECK(pg_try_advisory_lock(k)))", 0))
			// Cooking a CHECK is effect-free; executable row checks count each demand.
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(201)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(201)", "BOOL", true)
			run(referenceExec("INSERT INTO advisory_check VALUES(201),(201),(NULL)", 3))
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(201)", "BOOL", false)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(201)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(201)", "BOOL", false)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(201)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(201)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(201)", "BOOL", true)
			run(referenceExec("CREATE TABLE advisory_check_shared(hi integer,lo integer,CONSTRAINT advisory_shared CHECK(pg_try_advisory_lock_shared(hi,lo)))", 0))
			run(referenceExec("INSERT INTO advisory_check_shared VALUES(1,2),(1,2)", 2))
			advisorySessionExec(t, p.ctx, peer, "INSERT INTO advisory_check_shared VALUES(1,2)", 1)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(1,2)", "BOOL", false)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock_shared(1,2)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(1,2)", "BOOL", false)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock_shared(1,2)", "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(1,2)", "BOOL", false)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock_shared(1,2)", "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(1,2)", "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(1,2)", "BOOL", true)
			run(referenceExec("CREATE TABLE advisory_check_null(hi integer,lo integer,CONSTRAINT advisory_runtime_null CHECK(pg_try_advisory_lock_shared(hi,CASE WHEN pg_try_advisory_lock(206) THEN lo ELSE -lo END)))", 0))
			run(referenceExec("INSERT INTO advisory_check_null VALUES(NULL,2)", 1))
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(206)", "BOOL", false)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(206)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(206)", "BOOL", true)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(206)", "BOOL", true)
			run(referenceExec("CREATE TABLE advisory_check_wait(k bigint,CONSTRAINT advisory_wait CHECK(pg_advisory_lock(k) IS NOT NULL))", 0))
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_lock(208)", "VOID", "")
			advisorySessionExec(t, p.ctx, peer, "SET lock_timeout='150ms'", 0)
			referenceCheckRun(t, p.ctx, peer, b.recorder, referenceImplicitError("INSERT INTO advisory_check_wait VALUES(208)", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(208)", "BOOL", true)
			advisorySessionExec(t, p.ctx, peer, "INSERT INTO advisory_check_wait VALUES(208)", 1)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(208)", "BOOL", false)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(208)", "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(208)", "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(208)", "BOOL", true)
			run(referenceExec("CREATE TABLE advisory_check_failure(k bigint,accepted boolean,CONSTRAINT advisory_failure CHECK(pg_try_advisory_lock(k) AND accepted))", 0))
			run(referenceImplicitError("INSERT INTO advisory_check_failure VALUES(209,TRUE),(210,FALSE)", referenceCheckViolation("advisory_check_failure", "advisory_failure", "210, f")))
			advisorySessionValue(t, p.ctx, a, "SELECT count(*) FROM advisory_check_failure", "INT8", int64(0))
			for _, key := range []string{"209", "210"} {
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock("+key+")", "BOOL", false)
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock("+key+")", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock("+key+")", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock("+key+")", "BOOL", true)
			}
			if !b.pg {
				// Stored CHECK syntax is replayable, not serialized live ownership. Replay
				// recomputes volatile checks on the existing isolated stage session/manager.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), "pg_try_advisory_lock") || !strings.Contains(dump.String(), "pg_advisory_lock") {
					t.Fatal("Dump lost CHECK producer", dump.String())
				}
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_lock(208)", "VOID", "")
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_lock(208)", "VOID", "")
				ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
				err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes()))
				cancel()
				if err != nil {
					t.Fatal("stage CHECK used target manager or lacked admitted scope", err)
				}
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(208)", "BOOL", false)
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(208)", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(208)", "BOOL", false)
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(208)", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(208)", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(208)", "BOOL", true)
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_lock(212)", "VOID", "")
				err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"\nINSERT INTO advisory_check_failure VALUES(211,FALSE);"))
				want := referenceCheckViolation("advisory_check_failure", "advisory_failure", "211, f")
				want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
				referenceAssertDiagnostic(t, err, &want)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(212)", "BOOL", false)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(211)", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(211)", "BOOL", true)
				advisorySessionValue(t, p.ctx, a, "SELECT count(*) FROM advisory_check_wait", "INT8", int64(1))
				advisorySessionValue(t, p.ctx, a, "SELECT count(*) FROM advisory_check_failure", "INT8", int64(0))
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(212)", "BOOL", true)
				connector, err := stuntgres.NewDriver().OpenConnector("advisory/check-replay")
				if err != nil {
					t.Fatal(err)
				}
				target := sql.OpenDB(connector)
				defer target.Close()
				target.SetMaxOpenConns(1)
				target.SetMaxIdleConns(1)
				if err = stuntgres.Restore(p.ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.ctx, target, "SELECT count(*) FROM advisory_check_shared", "INT8", int64(3))
				advisorySessionValue(t, p.ctx, target, "SELECT pg_try_advisory_lock(208)", "BOOL", true)
				advisorySessionClear(t, p.ctx, target)
			}
			advisorySessionClear(t, p.ctx, a)
			advisorySessionClear(t, p.ctx, peer)
		})
	}
}
