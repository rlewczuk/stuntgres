package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendCurrentLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		backendReset(t, p, o, pid)
		stamp := backendStamp(t, p, b.PG, o, pid, false)
		birth := backendQuery(t, p, b.PG, o, 0, pid)[0][10]
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		if statActivityPID(t, p, peer) != pid || !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) {
			t.Fatal("borrower return retired physical backend")
		}
		activityExec(t, p, o, "CREATE TABLE bc_sentinel(v integer)")
		activityExec(t, p, o, "INSERT INTO bc_sentinel VALUES(37)")
		backendCurrentCancellation(t, p, b, pid)
		if b.PG {
			return
		}
		// A held ordinary recipe and already returned eager Rows have different
		// lifetimes across accepted replacement.
		held, err := o.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_get_backend_wal($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		retained, err := held.QueryContext(p.Ctx, pid)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if backendStamp(t, p, b.PG, o, pid, false) != nil || backendStamp(t, p, b.PG, o, pid, true) != nil {
			t.Fatal("Reset retained backend history")
		}
		old := backendRows(t, retained, b.PG, backendWALNames, backendWALTypes)
		if !reflect.DeepEqual(old[0][4], stamp) {
			t.Fatal("Reset changed retained Rows", old, stamp)
		}
		e := fixture.HeldStatement{Executor: o, Statement: held, SQL: "SELECT * FROM pg_catalog.pg_stat_get_backend_wal($1)"}
		if current := backendQuery(t, p, b.PG, e, 2, pid); current[0][4] != nil {
			t.Fatal("held execution retained pre-Reset provider cut", current)
		}
		if !reflect.DeepEqual(backendQuery(t, p, b.PG, o, 0, pid)[0][10], birth) {
			t.Fatal("Reset reminted actual birth")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if backendStamp(t, p, b.PG, o, pid, false) != nil {
			t.Fatal("Restore imported monitoring history")
		}
		if current := backendQuery(t, p, b.PG, e, 2, pid); current[0][4] != nil {
			t.Fatal("held execution imported Restore monitoring", current)
		}
		if backendCount(t, p, o, "SELECT v FROM bc_sentinel") != 37 {
			t.Fatal("user data restore")
		}
		backendReset(t, p, o, pid)
		stamp = backendStamp(t, p, b.PG, o, pid, false)
		for _, input := range []io.Reader{bytes.NewBufferString("not a dump"), io.MultiReader(bytes.NewReader(dump.Bytes()), bytes.NewBufferString("\nnot SQL")), backendReadFailure{}} {
			if err := stuntgres.Restore(p.Ctx, p.Native, input); err == nil {
				t.Fatal("invalid replacement accepted")
			}
			if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) || backendCount(t, p, o, "SELECT v FROM bc_sentinel") != 37 {
				t.Fatal("failed replacement changed published state")
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("precanceled restore accepted")
		}
		ctx, cancel = context.WithCancel(p.Ctx)
		if err := stuntgres.Restore(ctx, p.Native, &backendCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: cancel}); err == nil {
			t.Fatal("after-input cancel accepted")
		}
		cancel()
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) {
			t.Fatal("canceled restore changed history")
		}
		d := stuntgres.NewDriver()
		connector, err := d.OpenConnector("backend-current-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		target, err := destination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		targetPID := statActivityPID(t, p, target)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if backendStamp(t, p, b.PG, target, targetPID, false) != nil || backendCount(t, p, target, "SELECT v FROM bc_sentinel") != 37 {
			t.Fatal("fresh destination imported backend history")
		}
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) {
			t.Fatal("independent driver crossed provider boundary")
		}
		if err := peer.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		peer.Close()
		statActivityRetired(t, p, o, pid)
		if len(backendQuery(t, p, b.PG, o, 1, pid)) != 0 {
			t.Fatal("physical retirement retained IO")
		}
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), false, false)
	})
}

type backendReadFailure struct{}

func (backendReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("backend current input failure")
}

type backendCancelReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r *backendCancelReader) Read(dst []byte) (int, error) {
	n, err := r.Reader.Read(dst)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

// Each live operation must reach a genuine relation wait before cancellation.
// Join the row consumer before lock/session cleanup, including on failure.
func backendCurrentCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, pid int64) {
	t.Helper()
	observer := statActivityConn(t, p, b)
	target := backendQuery(t, p, b.PG, observer, 0, pid)[0]
	targetIO := backendStamp(t, p, b.PG, observer, pid, true)
	targetWAL := backendStamp(t, p, b.PG, observer, pid, false)
	for kind, name := range backendFunctionNames {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", name, prepared), func(t *testing.T) {
				c := statActivityConn(t, p, b)
				defer c.Close()
				readerPID := statActivityPID(t, p, c)
				readerBirth := statActivityGet(t, p, observer, readerPID, true)[10]
				readerReset := backendStamp(t, p, b.PG, observer, readerPID, false)
				q := fmt.Sprintf("SELECT f.* FROM %s(%d) f CROSS JOIN bc_sentinel s WHERE s.v=37", name, pid)
				var e fixture.Executor = c
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				locker := statActivityConn(t, p, b)
				defer locker.Close()
				holderPID := statActivityPID(t, p, locker)
				tx, err := locker.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE bc_sentinel IN ACCESS EXCLUSIVE MODE", 0)
				ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
				successful := false
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := e.QueryContext(ctx, q)
					if rows != nil {
						successful = rows.Next()
						if err == nil {
							err = rows.Err()
						}
						if closeErr := rows.Close(); err == nil {
							err = closeErr
						}
					}
					return fixture.RelationOutcome{Err: err}
				})
				joined := false
				defer func() {
					cancel()
					if !joined {
						await()
					}
				}()
				statActivityBlocker(t, p, observer, readerPID, holderPID)
				waiting := statActivityAwait(t, p, observer, readerPID, "relation")
				if waiting.text(14) != "Lock" || waiting.text(16) != "active" || waiting.text(20) != q {
					t.Fatal("getter never reached the genuine relation wait", waiting)
				}
				if n := backendCount(t, p, observer, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted AND locktype='relation' AND relation='bc_sentinel'::regclass AND mode='AccessShareLock'", readerPID); n != 1 {
					t.Fatal("missing admitted getter AccessShare wait", n)
				}
				if err := ctx.Err(); err != nil {
					t.Fatal("operation context was not live at the cancellation barrier", err)
				}
				cancel()
				out := await()
				joined = true
				if successful {
					t.Fatal("canceled admitted getter published a row")
				}
				if b.PG {
					fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal("ordinary in-flight cancellation", out.Err)
				}
				if n := backendCount(t, p, observer, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", readerPID); n != 0 {
					t.Fatal("cancellation retained reader wait ownership", n)
				}
				if n := backendCount(t, p, observer, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND locktype='relation' AND relation='bc_sentinel'::regclass AND mode='AccessExclusiveLock'", holderPID); n != 1 {
					t.Fatal("reader cancellation changed the holder grant", n)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal("release conflicting holder", err)
				}

				healthy := c
				var value int64
				err = healthy.QueryRowContext(p.Ctx, "SELECT v FROM bc_sentinel").Scan(&value)
				if err != nil {
					// lib/pq Go-context cancellation can retire its transport
					// (fixture.go SessionCancel contract). Nothing else may
					// turn a failed same-reader health probe into recovery.
					if !b.PG || !errors.Is(err, driver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
						t.Fatal("same-reader cancellation recovery", err)
					}
					if stmt != nil {
						stmt.Close()
					}
					c.Close()
					statActivityRetired(t, p, observer, readerPID)
					healthy = statActivityConn(t, p, b)
					defer healthy.Close()
					if statActivityPID(t, p, healthy) == readerPID {
						t.Fatal("retired reader was adopted as a healthy replacement")
					}
					if err := healthy.QueryRowContext(p.Ctx, "SELECT v FROM bc_sentinel").Scan(&value); err != nil {
						t.Fatal("replacement reader health", err)
					}
					e = healthy
					if prepared {
						replacement, err := healthy.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer replacement.Close()
						e = fixture.HeldStatement{Executor: healthy, Statement: replacement, SQL: q}
					}
				} else {
					if statActivityPID(t, p, healthy) != readerPID || !reflect.DeepEqual(statActivityGet(t, p, observer, readerPID, true)[10], readerBirth) || !reflect.DeepEqual(backendStamp(t, p, b.PG, observer, readerPID, false), readerReset) {
						t.Fatal("cancellation replaced a preserved reader lifetime")
					}
				}
				if value != 37 {
					t.Fatal("canceled getter changed the sentinel", value)
				}
				rows, err := e.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal("healthy getter reexecution", err)
				}
				fresh := backendRows(t, rows, b.PG, backendOutputNames[kind], backendOutputTypes[kind])
				switch kind {
				case 0:
					if len(fresh) != 1 || !reflect.DeepEqual(fresh[0][10], target[10]) {
						t.Fatal("healthy activity target lifetime", fresh)
					}
				case 1:
					backendIOCheck(t, fresh, b.PG)
				case 2:
					backendWALCheck(t, fresh, b.PG, true)
				}
				current := backendQuery(t, p, b.PG, observer, 0, pid)[0]
				for _, i := range []int{0, 1, 2, 3, 10, 17} {
					if !reflect.DeepEqual(current[i], target[i]) {
						t.Fatal("cancellation changed the monitored target identity", backendActivityNames[i], current[i], target[i])
					}
				}
				if !reflect.DeepEqual(backendStamp(t, p, b.PG, observer, pid, true), targetIO) || !reflect.DeepEqual(backendStamp(t, p, b.PG, observer, pid, false), targetWAL) {
					t.Fatal("cancellation changed monitored backend reset history")
				}
			})
		}
	}
}
