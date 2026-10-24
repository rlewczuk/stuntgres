package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalDropAdmission(t *testing.T) {
	for _, kind := range []string{"unprivileged_missing", "unprivileged_existing", "unprivileged_self", "current", "session", "superuser_missing"} {
		for _, held := range []bool{false, true} {
			for _, mode := range []string{"autocommit", "sql", "go"} {
				t.Run(fmt.Sprintf("%s/held=%v/%s", kind, held, mode), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"rd_actor", "rd_target", "rd_member", "rd_other_member", "rd_effective", "rd_missing"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							superuser := kind == "current" || kind == "session" || kind == "superuser_missing"
							actorDefinition := "CREATE ROLE rd_actor LOGIN"
							if superuser {
								actorDefinition += " SUPERUSER"
							}
							for _, q := range []string{actorDefinition, "CREATE ROLE rd_target", "CREATE ROLE rd_member", "CREATE ROLE rd_other_member", "CREATE TABLE rd_sentinel(i INT)", "GRANT USAGE ON SCHEMA public TO rd_actor", "GRANT SELECT,INSERT ON rd_sentinel TO rd_actor"} {
								run(b.DB, fixture.Exec(q, 0))
							}
							run(b.DB, fixture.Exec("INSERT INTO rd_sentinel VALUES(7)", 1))
							pool := roleTransactionLoginPool(t, p, b, "rd_actor")
							actor, err := pool.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { actor.Close() })
							t.Cleanup(func() { actor.ExecContext(p.Ctx, "ROLLBACK") })
							actorPID := roleTransactionT2PID(t, p, b, actor)
							current := "rd_actor"
							if kind == "session" {
								run(b.DB, fixture.Exec("CREATE ROLE rd_effective SUPERUSER", 0))
								run(actor, fixture.Exec("SET ROLE rd_effective", 0))
								current = "rd_effective"
							}
							run(actor, roleTransactionIdentity("rd_actor", current))
							target, heldRole := "rd_target", "rd_target"
							if kind == "current" || kind == "session" || kind == "unprivileged_self" {
								target, heldRole = "rd_actor", "rd_actor"
							}
							if kind == "unprivileged_missing" || kind == "superuser_missing" {
								target = "rd_missing"
							}
							query := "DROP ROLE " + target
							want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to drop role")
							want.Detail = "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles."
							switch kind {
							case "current":
								want = fixture.ControlsReferenceDiagnostic("55006", "current user cannot be dropped")
							case "session":
								want = fixture.ControlsReferenceDiagnostic("55006", "session user cannot be dropped")
							case "superuser_missing":
								want = fixture.ControlsReferenceDiagnostic("42704", `role "rd_missing" does not exist`)
							}
							var definition *sql.Stmt
							if held {
								definition, err = actor.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatal("quiet held Prepare", err)
								}
								t.Cleanup(func() { definition.Close() })
							}
							owner, finish := roleTransactionBegin(t, p, b, actor, mode)
							run(owner, fixture.Exec("INSERT INTO rd_sentinel VALUES(8)", 1))
							if mode != "autocommit" {
								run(owner, fixture.Exec("SAVEPOINT drop_refusal", 0))
							}
							var execute fixture.Executor = owner
							if held {
								execute = fixture.HeldStatement{Executor: owner, Statement: definition, SQL: query}
							}
							holder, holderPID := roleTransactionT2Conn(t, p, b)
							contender, contenderPID := roleTransactionT2Conn(t, p, b)
							run(holder, fixture.Exec("BEGIN", 0))
							run(holder, fixture.Exec("GRANT "+heldRole+" TO rd_member", 0))
							run(contender, fixture.Exec("BEGIN", 0))
							joinContender, contenderDone := roleTransactionT2Worker(t, p, contender, "GRANT "+heldRole+" TO rd_other_member", false)
							t.Cleanup(func() { holder.ExecContext(p.Ctx, "ROLLBACK") })
							roleTransactionT2Wait(t, p, b, contenderPID, holderPID, contenderDone)
							hold := fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=$1 AND locktype='object' AND database=0 AND classid=1260 AND objid=(SELECT oid FROM pg_roles WHERE rolname=$2) AND mode='ShareUpdateExclusiveLock' AND granted", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
							hold.Args = []any{holderPID, heldRole}
							run(b.DB, hold)
							completed := make(chan struct{})
							joinForbidden := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								defer close(completed)
								return fixture.RowExecute(ctx, execute, query, false)
							})
							// On any failure release the real holder before fixture joins.
							t.Cleanup(func() { holder.ExecContext(p.Ctx, "ROLLBACK") })
							outcome := roleTransactionDropRefusal(t, p, b, actorPID, holderPID, completed, joinForbidden)
							if outcome.Result != nil || outcome.Rows != nil {
								t.Fatal("refused DROP exposed a result")
							}
							errorStep := fixture.ErrorDiagnostic(query, want)
							errorStep.ErrorStage = "execute"
							fixture.State(t, outcome.Err, want.Code)
							fixture.CheckDiagnostic(t, outcome.Err, errorStep, "execute")
							pending := fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=1260 AND NOT granted", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
							pending.Args = []any{actorPID}
							run(b.DB, pending)
							unblocked := fixture.Select("SELECT $1=ANY(pg_blocking_pids($2)) AS blocked", []string{"blocked"}, []string{"BOOL"}, [][]any{{false}})
							unblocked.Args = []any{holderPID, actorPID}
							run(b.DB, unblocked)
							run(b.DB, hold)
							roleTransactionT2Wait(t, p, b, contenderPID, holderPID, contenderDone)
							if mode != "autocommit" {
								run(owner, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
								run(owner, fixture.Exec("ROLLBACK TO drop_refusal", 0))
								run(owner, fixture.Exec("RELEASE drop_refusal", 0))
							}
							run(owner, roleTransactionIdentity("rd_actor", current))
							run(owner, fixture.Select("SELECT i FROM rd_sentinel ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
							run(owner, fixture.Exec("INSERT INTO rd_sentinel VALUES(9)", 1))
							finish(true)
							run(holder, fixture.Exec("ROLLBACK", 0))
							roleTransactionT2Success(t, joinContender())
							run(contender, fixture.Exec("ROLLBACK", 0))
							run(b.DB, roleTransactionExists("rd_actor", true))
							run(b.DB, roleTransactionExists("rd_target", true))
							run(b.DB, roleTransactionExists("rd_missing", false))
							run(b.DB, fixture.Select("SELECT pg_has_role('rd_member','"+heldRole+"','MEMBER') AS first,pg_has_role('rd_other_member','"+heldRole+"','MEMBER') AS second", []string{"first", "second"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
							run(b.DB, fixture.Select("SELECT i FROM rd_sentinel ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}, {int64(9)}}))
							run(actor, fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
						})
					}
				})
			}
		}
	}
}

// The positive SUE contender has already proved this holder's real conflict.
// A forbidden DROP must complete with its own denial while that holder stays
// active; observing a queue is a failure, never an expected cancellation error.
func roleTransactionDropRefusal(t *testing.T, p *fixture.Pair, b fixture.Backend, actor, holder int, completed <-chan struct{}, join func() fixture.RelationOutcome) fixture.RelationOutcome {
	t.Helper()
	for {
		select {
		case <-completed:
			return join()
		default:
		}
		var blocked bool
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT $1=ANY(pg_blocking_pids($2))", holder, actor).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			t.Fatal("forbidden DROP entered the role-lock queue before admission refusal")
		}
		if err := p.Ctx.Err(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}
