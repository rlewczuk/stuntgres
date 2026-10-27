package reference_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"analyze_actor", "analyze_owner", "analyze_group"})
	fixture.SchemaReferenceCleanup(t, "analyze_hidden")
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { analyzeRun(t, p, b, b.DB, s) }
		for _, q := range []string{"CREATE ROLE analyze_actor", "CREATE ROLE analyze_owner", "CREATE ROLE analyze_group", "GRANT USAGE ON SCHEMA public TO analyze_actor,analyze_owner,analyze_group", "CREATE TABLE analyze_acl(a INT)", "CREATE TABLE analyze_allowed(a INT)", "ALTER TABLE analyze_allowed OWNER TO analyze_actor", "CREATE SCHEMA analyze_hidden", "CREATE TABLE analyze_hidden.h(a INT)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO analyze_acl VALUES(1),(1)", 2))
		run(fixture.Exec("INSERT INTO analyze_allowed VALUES(2),(2),(2)", 3))
		var notices []fixture.Diagnostic
		_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
		arun(fixture.Exec("SET ROLE analyze_actor", 0))
		defer actor.ExecContext(p.Ctx, "RESET ROLE")
		arun(fixture.Exec("ANALYZE analyze_acl,analyze_allowed", 0))
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to analyze "analyze_acl", skipping it`}
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
			t.Fatalf("denied notices=%#v", notices)
		}
		analyzeTuples(t, p, b, b.DB, "analyze_acl", -1)
		analyzeTuples(t, p, b, b.DB, "analyze_allowed", 3)
		arun(fixture.ErrorDiagnostic("ANALYZE analyze_hidden.h", analyzeError("42501", "permission denied for schema analyze_hidden")))
		attempt := func() {
			notices = nil
			arun(fixture.Prepared(fixture.Exec("ANALYZE analyze_acl", 0)))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			analyzeTuples(t, p, b, b.DB, "analyze_acl", 2)
		}
		run(fixture.Exec("GRANT MAINTAIN ON analyze_acl TO analyze_actor", 0))
		attempt()
		run(fixture.Exec("REVOKE MAINTAIN ON analyze_acl FROM analyze_actor", 0))
		run(fixture.Exec("GRANT MAINTAIN ON analyze_acl TO analyze_group", 0))
		run(fixture.Exec("GRANT analyze_group TO analyze_actor", 0))
		attempt()
		run(fixture.Exec("REVOKE analyze_group FROM analyze_actor", 0))
		run(fixture.Exec("ALTER TABLE analyze_acl OWNER TO analyze_owner", 0))
		run(fixture.Exec("GRANT analyze_owner TO analyze_actor", 0))
		attempt()
		run(fixture.Exec("REVOKE analyze_owner FROM analyze_actor", 0))
		var database, owner string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT datname,pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&database, &owner); err != nil {
			t.Fatal(err)
		}
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		run(fixture.Exec("ALTER DATABASE "+quote(database)+" OWNER TO analyze_actor", 0))
		defer func() {
			if _, err := b.DB.ExecContext(p.Ctx, "ALTER DATABASE "+quote(database)+" OWNER TO "+quote(owner)); err != nil {
				t.Error(err)
			}
		}()
		attempt()
		arun(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceAnalyzeCancellationLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				holder := analyzeConn(t, p, b)
				var notices []fixture.Diagnostic
				_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, rec, s) }
				run(fixture.Exec("CREATE TABLE analyze_locked(a INT,b INT)", 0))
				run(fixture.Exec("CREATE TABLE analyze_unlocked(a INT)", 0))
				analyzeInsert(t, p, b, worker, "analyze_locked", 14, 2, 4)
				run(fixture.Exec("INSERT INTO analyze_unlocked VALUES(7),(7)", 2))
				run(fixture.Exec("ANALYZE analyze_locked", 0))
				run(fixture.Exec("UPDATE analyze_locked SET a=2 WHERE a=1", 14))
				analyzeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				analyzeRun(t, p, b, holder, fixture.Exec("LOCK TABLE analyze_locked IN SHARE MODE", 0))
				s := fixture.Exec("ANALYZE (SKIP_LOCKED) analyze_locked,analyze_unlocked", 0)
				s.Prepared = prepared
				run(s)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping analyze of "analyze_locked" --- lock not available`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				analyzeTuples(t, p, b, worker, "analyze_unlocked", 2)
				analyzeWantEstimate(t, p, worker, "analyze_locked", "a=1", 14)
				q := "ANALYZE analyze_locked"
				h := analyzeHeld(t, p, worker, q)
				waiterPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, worker)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "analyze_locked")
				}
				fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, q, "(*relationRequest).wait", func(ctx context.Context) {
					if b.PG {
						fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock")
					}
				}, func(ctx context.Context) fixture.RelationOutcome {
					var out fixture.RelationOutcome
					if prepared {
						out.Result, out.Err = h.Statement.ExecContext(ctx)
					} else {
						out.Result, out.Err = worker.ExecContext(ctx, q)
					}
					return out
				})
				analyzeWantEstimate(t, p, worker, "analyze_locked", "a=1", 14)
				analyzeTuples(t, p, b, worker, "analyze_locked", 20)
				// A second real request must wait for the same blocker, then
				// complete normally after release (not merely pass after cancel).
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, worker)
				}
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var out fixture.RelationOutcome
					if prepared {
						out.Result, out.Err = h.Statement.ExecContext(ctx)
					} else {
						out.Result, out.Err = worker.ExecContext(ctx, q)
					}
					return out
				})
				fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock", finished)
				analyzeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
					t.Fatalf("ANALYZE result=%d,%v", n, err)
				}
				analyzeWantEstimate(t, p, worker, "analyze_locked", "a=2", 16)
			})
		})
	}
}
