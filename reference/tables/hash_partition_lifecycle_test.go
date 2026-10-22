package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceHashPartitionAtomicity(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_atomic(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_atomic_leaf PARTITION OF hp_atomic FOR VALUES WITH(MODULUS 8,REMAINDER 0)", 0))
		for _, suffix := range []string{"", " RETURNING k,payload"} {
			s := referenceRangeMiss("INSERT INTO hp_atomic VALUES(1,'valid'),(17,'missing')"+suffix, "hp_atomic", "k", "17")
			s.SelectRows = suffix != ""
			run(s)
			run(fixture.Select("SELECT k,payload FROM hp_atomic", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
		}
		run(fixture.Exec("CREATE TABLE hp_retry PARTITION OF hp_atomic FOR VALUES WITH(MODULUS 8,REMAINDER 4)", 0))
		run(fixture.Exec("INSERT INTO hp_atomic VALUES(1,'valid'),(17,'now_valid')", 2))
		run(fixture.Select("SELECT k FROM hp_retry", []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
	})
}

func TestReferenceHashPartitionAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hp_actor_direct", "hp_actor_prepared"})
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		setup := referenceHashRun(t, p, b, prepared)
		setup(fixture.Exec("CREATE TABLE hp_acl(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		setup(fixture.Exec("CREATE TABLE hp_acl_leaf PARTITION OF hp_acl FOR VALUES WITH(MODULUS 8,REMAINDER 0)", 0))
		conn, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		actor := "hp_actor_direct"
		if prepared {
			actor = "hp_actor_prepared"
		}
		run := func(s fixture.Step) {
			s.Query = strings.ReplaceAll(s.Query, "hp_actor", actor)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		run(fixture.Exec("CREATE ROLE hp_actor", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO hp_actor", 0))
		} else {
			// Native schema SQL grants are not admitted. Transfer only schema
			// authority; bootstrap-owned tables retain their independent ACLs.
			run(fixture.Exec("ALTER SCHEMA public OWNER TO hp_actor", 0))
		}
		run(fixture.Exec("GRANT INSERT ON hp_acl_leaf TO hp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hp_actor", 0))
		run(referenceRangeError("CREATE TABLE hp_denied PARTITION OF hp_acl FOR VALUES WITH(MODULUS 8,REMAINDER 4)", "42501", "must be owner of table hp_acl", fixture.Diagnostic{}))
		q := "CREATE TABLE hp_denied PARTITION OF hp_acl FOR VALUES WITH(REMAINDER 0)"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "modulus for hash partition must be specified", Position: referenceRangePosition(q, "WITH")})
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
		run(referenceRangeError("INSERT INTO hp_acl VALUES(1,'denied')", "42501", "permission denied for table hp_acl", fixture.Diagnostic{}))
		run(fixture.Exec("INSERT INTO hp_acl_leaf VALUES(1,'leaf')", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE INSERT ON hp_acl_leaf FROM hp_actor", 0))
		run(fixture.Exec("GRANT INSERT ON hp_acl TO hp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hp_actor", 0))
		run(referenceRangeError("INSERT INTO hp_acl_leaf VALUES(1,'denied')", "42501", "permission denied for table hp_acl_leaf", fixture.Diagnostic{}))
		run(fixture.Exec("INSERT INTO hp_acl VALUES(1,'parent')", 1))
		run(referenceRangeError("INSERT INTO hp_acl VALUES(17,'secret')", "23514", `no partition of relation "hp_acl" found for row`, fixture.Diagnostic{Schema: "public", Table: "hp_acl"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON hp_acl TO hp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hp_actor", 0))
		run(referenceRangeMiss("INSERT INTO hp_acl VALUES(17,'visible')", "hp_acl", "k", "17"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE hp_acl ENABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("CREATE POLICY hp_insert ON hp_acl FOR INSERT TO hp_actor WITH CHECK(true)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hp_actor", 0))
		run(referenceRangeError("INSERT INTO hp_acl VALUES(17,'hidden')", "23514", `no partition of relation "hp_acl" found for row`, fixture.Diagnostic{Schema: "public", Table: "hp_acl"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceHashPartitionCancellation(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_cancel(k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_cancel_zero PARTITION OF hp_cancel FOR VALUES WITH(MODULUS 8,REMAINDER 0)", 0))
		run(fixture.Exec("INSERT INTO hp_cancel VALUES(1)", 1))
		holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
		q := "CREATE TABLE hp_cancel_new PARTITION OF hp_cancel FOR VALUES WITH(MODULUS 8,REMAINDER 4)"
		var executor fixture.Executor = waiter
		if prepared {
			// Prepare before the blocker and execution timeout can affect this phase.
			stmt, err := waiter.PrepareContext(p.Ctx, q)
			if stmt != nil {
				t.Cleanup(func() {
					if err := stmt.Close(); err != nil {
						t.Error("held statement close:", err)
					}
				})
			}
			if err != nil || stmt == nil {
				t.Fatal("prepare held partition definition:", stmt, err)
			}
			executor = fixture.HeldStatement{Executor: waiter, Statement: stmt, SQL: q}
		}
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		if b.PG {
			fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE hp_cancel IN ACCESS EXCLUSIVE MODE", 0)
		} else {
			fixture.LockExec(t, p.Ctx, holder, "INSERT INTO hp_cancel VALUES(1)", 1)
		}
		// Keep the blocker until execution has completed, including on failure:
		// RowAsync cleanup joins before statement/connection cleanup releases it.
		if b.PG {
			pid, blocker := fixture.LockPID(t, p.Ctx, waiter), fixture.LockPID(t, p.Ctx, holder)
			done := fixture.RowAsync(t, p.Ctx, executor, q, false, false)
			fixture.RowWait(t, p.Ctx, pid, blocker)
			var ack bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ack); err != nil || !ack {
				t.Fatal(ack, err)
			}
			outcome := fixture.LockResult(t, done)
			s := referenceRangeError(q, "57014", "canceling statement due to user request", fixture.Diagnostic{})
			fixture.CheckDiagnostic(t, outcome.Err, s, "execute")
			if outcome.Result != nil {
				t.Fatal("canceled CREATE published a result")
			}
		} else {
			var parentOID uint32
			err := b.DB.QueryRowContext(p.Ctx, "SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='hp_cancel'").Scan(&parentOID)
			if err != nil || parentOID == 0 {
				t.Fatal("parent relation identity:", parentOID, err)
			}
			var holds int64
			err = b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE locktype='relation' AND relation=$1 AND mode='RowExclusiveLock' AND granted", parentOID).Scan(&holds)
			if err != nil || holds != 1 {
				t.Fatal("parent granted RowExclusiveLock:", holds, err)
			}
			// Observe the persistent hold before arming; do not race to observe a
			// transient waiter within the unchanged 50ms statement budget.
			timeoutArmed := true
			t.Cleanup(func() {
				if timeoutArmed {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					fixture.LockExec(t, ctx, waiter, "SET statement_timeout=0", 0)
				}
			})
			fixture.LockExec(t, p.Ctx, waiter, "SET statement_timeout='50ms'", 0)
			s := referenceRangeError(q, "57014", "canceling statement due to statement timeout", fixture.Diagnostic{})
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
			fixture.LockExec(t, p.Ctx, waiter, "SET statement_timeout=0", 0)
			timeoutArmed = false
		}
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hp_cancel_new'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE TYPE hp_cancel_new AS (probe INT)", 0))
		run(fixture.Exec("DROP TYPE hp_cancel_new", 0))
		run(referenceRangeRows("SELECT k FROM hp_cancel", 1))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec("INSERT INTO hp_cancel VALUES(17)", 1))
		run(referenceRangeRows("SELECT k FROM hp_cancel_new", 17))
	})
}
