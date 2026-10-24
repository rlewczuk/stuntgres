package reference_test

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsTransactionsTwoPhase(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		for _, direction := range []string{"UPDATE", "DELETE"} {
			t.Run(direction+"/"+completion, func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(10,1,'old')", 1))
					a, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					q := "DELETE FROM p"
					if direction == "UPDATE" {
						q = "UPDATE p SET id=2"
					}
					fixture.RowAffected(t, p.Ctx, a, q, true, 1)
					fixture.PreparedReferencePrepare(t, p, b, a, "foreign_action_holds")
					fixture.PreparedReferenceOne(t, p, waiter, "foreign_action_holds")
					// Probe interruption separately from the observed admission/release
					// witness below: neither observer must race the 100ms deadline.
					ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
					defer cancel()
					o := fixture.RowExecute(ctx, waiter, "UPDATE c SET note='later' WHERE id=10", false)
					if b.PG {
						got := fixture.ExtractDiagnostic(t, o.Err)
						// Heap coordinates vary; the action and relation must not.
						pattern := regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "c"$`)
						if !pattern.MatchString(got.Context) {
							t.Fatalf("prepared child interruption Context got %q", got.Context)
						}
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						want.Context = got.Context
						fixture.AssertDiagnostic(t, o.Err, &want)
					} else if !errors.Is(o.Err, context.DeadlineExceeded) || o.Result != nil {
						t.Fatal("prepared child probe did not time out without a result", o.Err, o.Result)
					}
					<-ctx.Done()
					ctxErr := ctx.Err()
					if ctxErr != context.DeadlineExceeded {
						t.Fatal("prepared child probe context", ctxErr)
					}
					cancel()
					if err := waiter.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.PreparedReferenceOne(t, p, a, "foreign_action_holds")
					control, writer := fixture.LockConnections(t, p.Ctx, b.DB)
					var waiterPID int
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, writer)
					}
					completed := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(workerCtx context.Context) fixture.RelationOutcome {
						defer close(completed)
						return fixture.RowExecute(workerCtx, writer, "UPDATE c SET note='later' WHERE id=10", true)
					})
					if b.PG {
						observeCtx, observeCancel := context.WithTimeout(p.Ctx, 3*time.Second)
						defer observeCancel()
						fixture.RowWait(t, observeCtx, waiterPID, 0)
					} else {
						fixture.NativeUpdateRowWait(t, p.Ctx, completed)
					}
					select {
					case <-completed:
						t.Fatal("prepared child writer completed before prepared resolution")
					default:
					}
					fixture.LockExec(t, p.Ctx, control, completion+" PREPARED 'foreign_action_holds'", 0)
					outcome := join()
					if outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
					fixture.PreparedReferenceAbsent(t, p, control, "foreign_action_holds")
					count := int64(1)
					if direction == "DELETE" && completion == "COMMIT" {
						count = 0
					}
					fixture.LockAffected(t, outcome.Result, count)
					var rows [][]any
					if count != 0 {
						pid := int64(1)
						if completion == "COMMIT" {
							pid = 2
						}
						rows = [][]any{{int64(10), pid, "later"}}
					}
					run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, rows))
					parents := []int64{1}
					if completion == "COMMIT" {
						parents = nil
						if direction == "UPDATE" {
							parents = []int64{2}
						}
					}
					run(foreignKeyIntRows("SELECT id FROM p", "id", parents...))
				})
			})
		}
	}
}

func TestReferenceForeignActionsLifecycleSkippedCreate(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		q := "CREATE TABLE IF NOT EXISTS c(pid INT CONSTRAINT ignored_fk REFERENCES p ON DELETE SET NULL(missing))"
		if b.PG {
			run(fixture.Exec(q, 0))
		} else {
			script := "CREATE TABLE p(id INT PRIMARY KEY);CREATE TABLE c(pid INT);INSERT INTO p VALUES(1);INSERT INTO c VALUES(1);" + q + ";"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal("skipped action attachment was bound", err)
			}
		}
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ignored_fk'", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Exec("DELETE FROM p", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
	})
}

func TestReferenceForeignActionsLifecycleCapturedAuthority(t *testing.T) {
	const alpha, beta = "fa_capture_alpha", "fa_capture_beta"
	fixture.RoleReferenceCleanup(t, []string{alpha, beta})
	fixture.SchemaReferenceCleanup(t, alpha, beta)
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE " + alpha, "CREATE ROLE " + beta, "CREATE SCHEMA " + alpha + " AUTHORIZATION " + alpha, "CREATE SCHEMA " + beta + " AUTHORIZATION " + beta} {
			run(fixture.Exec(q, 0))
		}
		decl := []string{"SET SESSION AUTHORIZATION " + alpha, "CREATE TABLE c(pid INT DEFAULT 1 CONSTRAINT captured_action_fk REFERENCES p ON UPDATE CASCADE ON DELETE SET DEFAULT)", "INSERT INTO c VALUES(2)", "CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1),(2)"}
		other := []string{"SET SESSION AUTHORIZATION " + beta, "CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(9)", "SET SESSION AUTHORIZATION postgres"}
		if b.PG {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			step := func(q string) {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
					if strings.Contains(q, "(1),(2)") {
						n = 2
					}
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, n))
			}
			for _, i := range []int{0, 3, 4, 1, 2} {
				step(decl[i])
			}
			for _, q := range other {
				step(q)
			}
		} else {
			// Restore replaces persistent state; recreate schemas inside its
			// isolated candidate, retaining declaration-time $user/owner/path.
			prefix := []string{"CREATE SCHEMA " + alpha + " AUTHORIZATION " + alpha, "CREATE SCHEMA " + beta + " AUTHORIZATION " + beta}
			all := append(append(prefix, decl...), other...)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(strings.Join(all, ";")+";")); err != nil {
				t.Fatal(err)
			}
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Exec("UPDATE "+alpha+".p SET id=3 WHERE id=2", 1))
		run(foreignKeyIntRows("SELECT pid FROM "+alpha+".c", "pid", 3))
		run(foreignKeyIntRows("SELECT id FROM "+beta+".p", "id", 9))
		run(fixture.Exec("DELETE FROM "+alpha+".p WHERE id=3", 1))
		run(foreignKeyIntRows("SELECT pid FROM "+alpha+".c", "pid", 1))
		run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='captured_action_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (pid) REFERENCES " + alpha + ".p(id) ON UPDATE CASCADE ON DELETE SET DEFAULT"}}))
	})
}

func TestReferenceForeignActionsCancellationReplacement(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE,note TEXT)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO c VALUES(1,'old')", 1))
				if b.PG {
					// Native replacement APIs have no PostgreSQL counterpart. The
					// shared SQL action/cancellation scenarios cover PostgreSQL waits.
					run(fixture.Exec("DELETE FROM p", 1))
					run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
					return
				}
				dump := fixture.CheckDump(t, p)
				a, z := fixture.LockConnections(t, p.Ctx, b.DB)
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RowAffected(t, p.Ctx, holder, "UPDATE c SET note='private'", false, 1)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowExecute(ctx, z, "DELETE FROM p", true)
				})
				// Observe the actual initiating row grant through public NOWAIT.
				// The holder prevents completion of the ensuing child mutation.
				ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
				for {
					tx, e := b.DB.BeginTx(ctx, nil)
					if e != nil {
						cancel()
						t.Fatal(e)
					}
					rows, e := tx.QueryContext(ctx, "SELECT id FROM p FOR KEY SHARE NOWAIT")
					if rows != nil {
						for rows.Next() {
						}
						rows.Close()
					}
					tx.Rollback()
					if e != nil {
						want := foreignKeyError("55P03", `could not obtain lock on row in relation "p"`, "", "", "")
						fixture.AssertDiagnostic(t, e, &want)
						break
					}
					if ctx.Err() != nil {
						cancel()
						t.Fatal("initiating action grant not observed", ctx.Err())
					}
					runtime.Gosched()
				}
				cancel()
				switch replacement {
				case "reset":
					err = stuntgres.Reset(p.Ctx, p.Native)
				case "restore":
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump))
				}
				if err != nil {
					t.Fatal(err)
				}
				o := join()
				fixture.TransactionOrdinary(t, o.Err, "stuntgres: catalog replaced before publication")
				_ = holder.Rollback()
				if replacement != "restore" {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1,'replacement')", 1))
				}
				run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
				note := "old"
				if replacement != "restore" {
					note = "replacement"
				}
				run(fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), note}}))
				run(fixture.Exec("DELETE FROM p", 1))
				run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
			})
		})
	}
}
