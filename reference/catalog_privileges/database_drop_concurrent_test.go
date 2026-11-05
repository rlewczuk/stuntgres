package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 dbcommands.c/get_db_info re-fetches after the database AX wait.
// Once the earlier DROP removes the tuple, dropdb takes its ordinary missing
// branch: plain DROP is 3D000; IF EXISTS is NOTICE 00000 and affected zero.
// A transactional RENAME supplies a genuine AX holder, rolled back only after
// both independent DROP issuers are observed in their actual queue order.
func TestReferenceDatabaseDropConcurrent(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, missingOK := range []bool{false, true} {
			label := "plain"
			if missingOK {
				label = "if_exists"
			}
			t.Run(mode+"/"+label, func(t *testing.T) {
				const target, renamed, peer = "dd_concurrent_target", "dd_concurrent_renamed", "dd_concurrent_peer"
				names := []string{target, renamed, peer}
				fixture.DatabaseReferenceCleanup(t, &names)
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						observer, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer observer.Close()
						c := &databaseCreateCase{t, p, backend, observer, &names}
						c.create(peer, "", false)
						c.run(fixture.Exec("ALTER DATABASE "+peer+" SET application_name='drop_concurrent_peer'", 0))
						peerDB, peerRaw, peerRecord := c.pool(peer, "")
						fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Exec("CREATE TABLE dd_peer_value(v integer)", 0))
						fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Exec("INSERT INTO dd_peer_value VALUES(22)", 1))
						peerOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", peer)
						defaultName := c.scalar("SELECT current_database()")
						defaultOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", defaultName)
						c.run(fixture.Exec("SET application_name='drop_concurrent_default'", 0))

						var firstNotices, secondNotices []fixture.Diagnostic
						_, first, firstRecord := fixture.NoticeReferencePool(t, p, backend.PG, &firstNotices)
						_, second, secondRecord := fixture.NoticeReferencePool(t, p, backend.PG, &secondNotices)
						firstSQL, secondSQL := "DROP DATABASE "+target, "DROP DATABASE "+target
						if missingOK {
							secondSQL = "DROP DATABASE IF EXISTS " + target
						}
						firstCase := &databaseCreateCase{t, p, backend, first, &names}
						secondCase := &databaseCreateCase{t, p, backend, second, &names}
						firstRoute := databaseDropPrepare(firstCase, mode, firstSQL)
						secondRoute := databaseDropPrepare(secondCase, mode, secondSQL)
						databaseDropNotice(t, &firstNotices, target, 0)
						databaseDropNotice(t, &secondNotices, target, 0)
						firstPID := databaseDropConcurrentPID(t, p.Ctx, first)
						secondPID := databaseDropConcurrentPID(t, p.Ctx, second)
						observerPID := databaseDropConcurrentPID(t, p.Ctx, observer)
						if firstPID == secondPID || firstPID == observerPID || secondPID == observerPID {
							t.Fatal("DROP issuers and observer must be independent physical owners")
						}

						var previousOID string
						// The held routes retain the SAME two physical statements across
						// both real races, including successful removal and recreation.
						for range 2 {
							c.create(target, "", false)
							oid := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", target)
							if oid == previousOID {
								t.Fatal("same-name recreation retained the removed incarnation")
							}
							previousOID = oid
							databaseDropConcurrentRound(t, p, backend, observer, firstRoute, secondRoute, firstRecord, secondRecord, firstSQL, secondSQL, firstPID, secondPID, oid, target, renamed, missingOK)
							c.absent(target)
							c.absent(renamed)
							databaseDropNotice(t, &firstNotices, target, 0)
							noticeCount := 0
							if missingOK {
								noticeCount = 1
							}
							databaseDropNotice(t, &secondNotices, target, noticeCount)
							for _, issuer := range []struct {
								conn   *sql.Conn
								record *fixture.Recorder
							}{{first, firstRecord}, {second, secondRecord}} {
								fixture.RunStep(t, p.Ctx, issuer.conn, issuer.record, fixture.Select("SELECT 42 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
							}
							c.run(fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE locktype='object' AND classid=1262 AND objid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
							c.run(fixture.Select("SELECT count(*) AS n FROM pg_stat_activity WHERE datname=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, target))
							fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Select("SELECT v FROM dd_peer_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
							fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"drop_concurrent_peer"}}))
							if c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", peer) != peerOID || c.scalar("SELECT current_database()") != defaultName || c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", defaultName) != defaultOID || c.scalar("SHOW application_name") != "drop_concurrent_default" {
								t.Fatal("queued DROP changed unrelated peer/default identity or settings")
							}
						}
						databaseDropClose(t, peerDB, peerRaw)
					})
				}
			})
		}
	}
}

type databaseDropConcurrentWorker struct {
	executor        fixture.Executor
	query           string
	ctx             context.Context
	finished        chan struct{}
	result          sql.Result
	err             error
	started, joined bool
}

func (w *databaseDropConcurrentWorker) start() {
	w.started = true
	go func() {
		w.result, w.err = w.executor.ExecContext(w.ctx, w.query)
		close(w.finished)
	}()
}

func (w *databaseDropConcurrentWorker) join(t *testing.T) {
	t.Helper()
	if !w.started || w.joined {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	select {
	case <-w.finished:
		w.joined = true
	case <-ctx.Done():
		t.Fatal("queued DROP did not finish", w.query, ctx.Err())
	}
}

func databaseDropConcurrentRound(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, firstRoute, secondRoute databaseDropRoute, firstRecord, secondRecord *fixture.Recorder, firstSQL, secondSQL string, firstPID, secondPID int, oid, target, renamed string, missingOK bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	prepare := func(route databaseDropRoute, query string) fixture.Executor {
		t.Helper()
		if route.mode != "fresh" {
			return route.held
		}
		stmt, err := route.held.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal("effect-free fresh preparation", err)
		}
		deferClose := func() {
			if err := stmt.Close(); err != nil {
				t.Error("fresh DROP statement close", err)
			}
		}
		t.Cleanup(deferClose)
		return fixture.HeldStatement{Executor: route.held, Statement: stmt, SQL: query}
	}
	first := &databaseDropConcurrentWorker{executor: prepare(firstRoute, firstSQL), query: firstSQL, ctx: context.WithValue(ctx, fixture.RecorderKey{}, firstRecord), finished: make(chan struct{})}
	second := &databaseDropConcurrentWorker{executor: prepare(secondRoute, secondSQL), query: secondSQL, ctx: context.WithValue(ctx, fixture.RecorderKey{}, secondRecord), finished: make(chan struct{})}
	holder, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holderPID := databaseDropConcurrentPID(t, ctx, holder)
	if holderPID == firstPID || holderPID == secondPID {
		t.Fatal("AX holder borrowed a DROP issuer")
	}
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := false
	// Register cancel/release/join before either worker can enter lock admission.
	// Each join must run even when the other reports a fatal liveness failure.
	defer second.join(t)
	defer first.join(t)
	defer func() {
		cancel()
		if !rolledBack {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Error("AX holder cleanup", err)
			}
		}
	}()
	fixture.RunStep(t, ctx, tx, b.Recorder, fixture.Exec("ALTER DATABASE "+target+" RENAME TO "+renamed, 0))
	fixture.RunStep(t, ctx, observer, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=1262 AND objid=$2::oid AND objsubid=0 AND mode='AccessExclusiveLock' AND granted", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, holderPID, oid))
	first.start()
	databaseDropConcurrentPending(t, ctx, observer, first, firstPID, holderPID, 0, oid)
	second.start()
	// The earlier pending AX is a soft blocker as well as the holder's hard
	// blocker. This distinguishes first/second queue order, not launch timing.
	databaseDropConcurrentPending(t, ctx, observer, second, secondPID, holderPID, firstPID, oid)
	fixture.RunStep(t, ctx, observer, b.Recorder, fixture.Select("SELECT datname FROM pg_database WHERE oid=$1::oid", []string{"datname"}, []string{"NAME"}, [][]any{{target}}, oid))
	if err := tx.Rollback(); err != nil {
		t.Fatal("release genuine database AX", err)
	}
	rolledBack = true
	first.join(t)
	second.join(t)
	databaseDropConcurrentSuccess(t, first)
	if missingOK {
		databaseDropConcurrentSuccess(t, second)
	} else {
		if second.result != nil {
			t.Fatal("failed second DROP exposed a result")
		}
		want := databaseCreateDiagnostic("3D000", "database \""+target+"\" does not exist")
		fixture.AssertDiagnostic(t, second.err, &want)
	}
}

func databaseDropConcurrentPending(t *testing.T, ctx context.Context, observer *sql.Conn, worker *databaseDropConcurrentWorker, waiter, holder, earlier int, oid string) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, fixture.Liveness)
	defer cancel()
	for {
		select {
		case <-worker.finished:
			t.Fatal("DROP finished before its exact queued database AX was observed", worker.err)
		case <-bounded.Done():
			t.Fatal("queued database AX admission", bounded.Err())
		default:
		}
		var pending bool
		q := `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=1262 AND objid=$2::oid AND objsubid=0 AND mode='AccessExclusiveLock' AND NOT granted) AND $3=ANY(pg_blocking_pids($1))`
		args := []any{waiter, oid, holder}
		if earlier != 0 {
			q += ` AND $4=ANY(pg_blocking_pids($1))`
			args = append(args, earlier)
		}
		if err := observer.QueryRowContext(bounded, q, args...).Scan(&pending); err != nil {
			t.Fatal("observe exact database AX issuer and blocker", err)
		}
		if pending {
			return
		}
		runtime.Gosched()
	}
}

func databaseDropConcurrentPID(t *testing.T, ctx context.Context, conn *sql.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid == 0 {
		t.Fatal("physical database issuer PID", pid, err)
	}
	return pid
}

func databaseDropConcurrentSuccess(t *testing.T, worker *databaseDropConcurrentWorker) {
	t.Helper()
	if worker.err != nil || worker.result == nil {
		t.Fatal("queued DROP must succeed", worker.query, worker.err)
	}
	if count, err := worker.result.RowsAffected(); err != nil || count != 0 {
		t.Fatal("queued DROP affected count", count, err)
	}
	if _, err := worker.result.LastInsertId(); err == nil {
		t.Fatal("DROP unexpectedly supports LastInsertId")
	}
}
