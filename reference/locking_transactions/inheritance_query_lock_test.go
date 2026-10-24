package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func inheritanceQueryLocks(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, bool)) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					for _, step := range []fixture.Step{
						fixture.Exec("CREATE TABLE p(id INT,v INT)", 0), fixture.Exec("CREATE TABLE q(prefix BOOL,id INT)", 0),
						fixture.Exec("CREATE TABLE l() INHERITS(p)", 0), fixture.Exec("CREATE TABLE r() INHERITS(p)", 0),
						fixture.Exec("CREATE TABLE child(local TEXT) INHERITS(q,l,r)", 0),
						fixture.Exec("INSERT INTO p VALUES(1,10)", 1), fixture.Exec("INSERT INTO child VALUES(true,2,20,'child')", 1),
						fixture.Exec("CREATE VIEW inherited AS SELECT id,v FROM p", 0),
					} {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
					}
					body(t, p, b, a, c, prepared)
				})
			}
		})
	}
}

// Public runtime observation follows the existing conversion cancellation fixture:
// PostgreSQL observes the actual blocker; native observes an actually parked lock
// request, not a sleep or middleware entry assumed to imply admission.
func inheritanceQueryWait(t *testing.T, p *fixture.Pair, b fixture.Backend, holder, waiter int, completed ...<-chan fixture.RelationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	if b.PG {
		fixture.RowWait(t, ctx, waiter, holder)
		return
	}
	profiles := make([]runtime.StackRecord, 128)
	var early <-chan fixture.RelationOutcome
	if len(completed) != 0 {
		early = completed[0]
	}
	for ctx.Err() == nil {
		select {
		case out := <-early:
			if out.Err != nil {
				t.Fatalf("inheritance scanner exited before row-lock park: %v", out.Err)
			}
			var values []int64
			if out.Rows != nil {
				for out.Rows.Next() {
					var value int64
					if err := out.Rows.Scan(&value); err != nil {
						t.Fatal("early scanner row", err)
					}
					values = append(values, value)
				}
				err := out.Rows.Err()
				out.Rows.Close()
				if err != nil {
					t.Fatal("early scanner rows", err)
				}
			}
			t.Fatalf("inheritance scanner completed before row-lock park: returnedRows=%t values=%v", out.Rows != nil, values)
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			for {
				frame, more := frames.Next()
				if strings.Contains(frame.Function, ".(*relationRequest).wait") {
					return
				}
				if !more {
					break
				}
			}
		}
		runtime.Gosched()
	}
	t.Fatal("native inheritance lock request did not park", ctx.Err())
}

func TestReferenceInheritanceQueryRowMarks(t *testing.T) {
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		for _, strength := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
			for _, source := range []string{"p", "inherited", "(SELECT id,v FROM p)"} {
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.QueryLockRows(t, p, b, holder, "SELECT s.v FROM "+source+" s WHERE s.id=2 FOR "+strength+" OF s", prepared, "v", 20)
				fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM child WHERE id=2 FOR UPDATE NOWAIT", prepared, "child")
				fixture.QueryLockRows(t, p, b, c, "SELECT v FROM ONLY p FOR UPDATE NOWAIT", prepared, "v", 10)
				fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id FOR UPDATE SKIP LOCKED", prepared, "v", 10)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
		}
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT a.v FROM p a JOIN p b ON a.id=b.id WHERE a.id=2 FOR UPDATE OF a,b", prepared, "v", 20)
		fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM child WHERE id=2 FOR UPDATE NOWAIT", prepared, "child")
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		// Lock the preserved side only; the absent side is not a physical NULL row.
		fixture.QueryLockRows(t, p, b, a, "SELECT a.v FROM p a LEFT JOIN p b ON b.id=99 ORDER BY a.id FOR UPDATE OF a", prepared, "v", 10, 20)
	})
}

func TestReferenceInheritanceQueryLockRecheck(t *testing.T) {
	for _, action := range []string{"UPDATE child SET v=30 WHERE id=2", "UPDATE child SET id=3,v=30 WHERE id=2", "DELETE FROM child WHERE id=2"} {
		t.Run(action, func(t *testing.T) {
			inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(action, 1))
				ap, bp := 0, 0
				if b.PG {
					ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
				}
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowQuery(ctx, c, "SELECT v FROM p WHERE id=2 ORDER BY v FOR UPDATE", prepared, b.Recorder.RawBackend)
				})
				inheritanceQueryWait(t, p, b, ap, bp)
				if err := holder.Commit(); err != nil {
					t.Fatal(err)
				}
				out := await()
				if action == "UPDATE child SET v=30 WHERE id=2" {
					fixture.RowValues(t, out, 30)
				} else {
					fixture.RowValues(t, out)
				}
			})
		})
	}
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		reader, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Rollback()
		fixture.QueryLockRows(t, p, b, reader, "SELECT v FROM p WHERE id=2", prepared, "v", 20)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("UPDATE child SET v=30 WHERE id=2", 1))
		fixture.QueryLockRows(t, p, b, reader, "SELECT v FROM p WHERE id=2 FOR KEY SHARE", prepared, "v", 20)
		out := fixture.RowQuery(p.Ctx, reader, "SELECT v FROM p WHERE id=2 FOR UPDATE", prepared, b.Recorder.RawBackend)
		failure := fixture.QueryLockErrorOutcome(t, out, b.PG, "v", nil)
		fixture.AssertDiagnostic(t, failure, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
	})
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE recipe_parent(id INT,g INT GENERATED ALWAYS AS(id+1) VIRTUAL,physical OID GENERATED ALWAYS AS(tableoid) VIRTUAL)", 0),
			fixture.Exec("CREATE TABLE recipe_child(g INT GENERATED ALWAYS AS(id+100) VIRTUAL) INHERITS(q,recipe_parent)", 0),
			fixture.Exec("INSERT INTO recipe_child(prefix,id) VALUES(true,2)", 1),
		} {
			fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
		}
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("UPDATE recipe_child SET id=3 WHERE id=2", 1))
		ap, bp := 0, 0
		if b.PG {
			ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
		}
		query := "SELECT CASE WHEN physical='recipe_child'::regclass::oid THEN g ELSE -1 END AS v FROM recipe_parent WHERE id>=2 ORDER BY id FOR UPDATE"
		early := make(chan fixture.RelationOutcome, 1)
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			out := fixture.RowQuery(ctx, c, query, prepared, b.Recorder.RawBackend)
			early <- out
			return out
		})
		inheritanceQueryWait(t, p, b, ap, bp, early)
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.RowValues(t, await(), 4)
	})
}

func TestReferenceInheritanceQueryRelationWait(t *testing.T) {
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE ONLY child IN ACCESS EXCLUSIVE MODE", 0))
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		ap, bp := 0, 0
		if b.PG {
			ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
		}
		await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowQuery(ctx, c, "SELECT v FROM p ORDER BY id", prepared, b.Recorder.RawBackend)
		})
		inheritanceQueryWait(t, p, b, ap, bp)
		if b.PG {
			pid := bp
			var signaled bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
				t.Fatal(signaled, err)
			}
		} else {
			cancel()
		}
		out := await()
		err := fixture.QueryLockErrorOutcome(t, out, b.PG, "v", nil)
		if b.PG {
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id", prepared, "v", 10, 20)
		fixture.QueryLockRows(t, p, b, a, "SELECT v FROM child FOR UPDATE NOWAIT", prepared, "v", 20)
	})
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		if !b.PG {
			// A rolled-back private inherited table leaves the original child intact.
			var before, after int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 'child'::regclass::oid").Scan(&before); err != nil {
				t.Fatal(err)
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, a)
			step := fixture.Exec("CREATE TABLE native_unpublished() INHERITS(p)", 0)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, step)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.TableLikeAbsent(func(step fixture.Step) {
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}, "native_unpublished", prepared)
			if err := a.QueryRowContext(p.Ctx, "SELECT 'child'::regclass::oid").Scan(&after); err != nil || before != after {
				t.Fatal("rolled-back CREATE changed original child identity", before, after, err)
			}
			fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id", prepared, "v", 10, 20)
			fixture.QueryLockRows(t, p, b, c, "SELECT v FROM ONLY p", prepared, "v", 10)
		}
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE ONLY child IN ACCESS EXCLUSIVE MODE", 0))
		var original, replacement int64
		if err := holder.QueryRowContext(p.Ctx, "SELECT 'child'::regclass::oid").Scan(&original); err != nil {
			t.Fatal(err)
		}
		ap, bp := 0, 0
		if b.PG {
			ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
		}
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowQuery(ctx, c, "SELECT v FROM p ORDER BY id", prepared, b.Recorder.RawBackend)
		})
		inheritanceQueryWait(t, p, b, ap, bp)
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("DROP TABLE child", 0))
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("CREATE TABLE child() INHERITS(p)", 0))
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("INSERT INTO child VALUES(9,9)", 1))
		if err := holder.QueryRowContext(p.Ctx, "SELECT 'child'::regclass::oid").Scan(&replacement); err != nil {
			t.Fatal(err)
		}
		if original <= 0 || replacement <= 0 || original == replacement {
			t.Fatal("replacement did not receive distinct relation identity", original, replacement)
		}
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		out := await()
		if out.Stmt != nil {
			defer out.Stmt.Close()
		}
		var values []int64
		err := out.Err
		if out.Rows != nil {
			defer out.Rows.Close()
			for out.Rows.Next() {
				var value int64
				if e := out.Rows.Scan(&value); e != nil {
					t.Fatal(e)
				}
				if value == 9 {
					t.Fatal("in-flight scan retargeted the same-name replacement")
				}
				values = append(values, value)
			}
			err = out.Rows.Err()
			out.Rows.Close()
		}
		if b.PG {
			// PostgreSQL keeps the parent row but skips the removed child.
			if err != nil || len(values) != 1 || values[0] != 10 {
				t.Fatalf("replacement scan got rows=%v error=%v, want [10] with no error", values, err)
			}
		} else {
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			if len(values) != 0 {
				t.Fatalf("captured descendant returned rows after replacement: %v", values)
			}
		}
		fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id", prepared, "v", 10, 9)
		fixture.QueryLockRows(t, p, b, c, "SELECT v FROM ONLY p", prepared, "v", 10)
	})
}
