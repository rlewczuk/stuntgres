package core_test

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func sslCoreLogin(t *testing.T, e *core.Engine, name string) *session.Session {
	t.Helper()
	e.Mu.Lock()
	role, ok := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()).Lookup(name)
	e.Mu.Unlock()
	if !ok {
		t.Fatal("missing authenticated role", name)
	}
	s, err := enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: enginetest.NextSessionID()}, role.Ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestPGStatSSLConcurrentCapture(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	monitor := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, admin,
		"CREATE ROLE ssl_race_login LOGIN", "CREATE ROLE ssl_race_reader",
		"GRANT ssl_race_login TO ssl_race_reader", "CREATE TABLE ssl_race_data(v integer)",
		"GRANT USAGE ON SCHEMA public TO ssl_race_login", "GRANT ALL ON ssl_race_data TO ssl_race_login")
	target := sslCoreLogin(t, e, "ssl_race_login")
	oldPID := target.SessionToken.ID()
	activityOwnerRun(t, e, target, "BEGIN", "INSERT INTO ssl_race_data VALUES(7)")
	activityOwnerRun(t, e, monitor, "SET ROLE ssl_race_reader", "BEGIN")
	sslAST, sslDescription := enginetest.PreparedDescription(t, e, monitor, "SELECT * FROM pg_stat_ssl ORDER BY pid")
	activityAST, activityDescription := enginetest.PreparedDescription(t, e, monitor, "SELECT pid FROM pg_stat_activity ORDER BY pid")
	started, denied, restored := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop, done := make(chan struct{}), make(chan struct{})
	var workerErr error
	var old, frozen, initialMembership [][]sqltype.Value
	go func() {
		defer close(done)
		first, sawDenied, sawRestored := true, false, false
		for {
			select {
			case <-stop:
				return
			default:
			}
			result, err := enginetest.PreparedExecute(e, monitor, sslAST, &sslDescription)
			if err != nil {
				workerErr = err
				return
			}
			rows, err := sslCoreRows(t.Context(), result)
			if err == nil {
				err = sslCoreValidate(rows)
			}
			if err != nil {
				workerErr = err
				return
			}
			if len(rows) > 1 {
				workerErr = fmt.Errorf("successor/unrelated identity entered retained SSL cut: %v", rows)
				return
			}
			if len(rows) == 1 {
				pid, err := rows[0][0].Integer()
				if err != nil || uint64(pid) != uint64(oldPID) {
					workerErr = fmt.Errorf("SSL retained PID %d want %d: %v", pid, oldPID, err)
					return
				}
				if sawDenied && !sawRestored {
					sawRestored = true
					close(restored)
				}
			} else if !sawDenied {
				sawDenied = true
				close(denied)
			}
			activity, err := enginetest.PreparedExecute(e, monitor, activityAST, &activityDescription)
			if err != nil {
				workerErr = err
				return
			}
			members, err := sslCoreRows(t.Context(), activity)
			if err != nil {
				workerErr = err
				return
			}
			seen := make(map[int64]bool)
			for _, row := range members {
				pid, err := row[0].Integer()
				if err != nil || pid <= 0 || seen[pid] {
					workerErr = fmt.Errorf("activity cut duplicate/invalid identity %d: %v", pid, err)
					return
				}
				seen[pid] = true
			}
			if !seen[int64(oldPID)] {
				workerErr = fmt.Errorf("retained activity lost physical target")
				return
			}
			if first {
				if len(rows) != 1 {
					workerErr = fmt.Errorf("initial inherited capture absent")
					return
				}
				old = rows
				frozen = [][]sqltype.Value{append([]sqltype.Value(nil), rows[0]...)}
				initialMembership = members
				first = false
				close(started)
			} else if !reflect.DeepEqual(members, initialMembership) || !reflect.DeepEqual(old, frozen) {
				workerErr = fmt.Errorf("retained result/canonical membership mutated")
				return
			}
			runtime.Gosched()
		}
	}()
	joined := false
	stopWorker := func() {
		if !joined {
			close(stop)
			<-done
			joined = true
		}
	}
	defer stopWorker()
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	await := func(barrier <-chan struct{}) {
		t.Helper()
		select {
		case <-barrier:
		case <-done:
			t.Fatal("concurrent SSL capture stopped", workerErr)
		case <-ctx.Done():
			t.Fatal("concurrent SSL authority barrier", ctx.Err())
		}
	}
	await(started)
	for range 16 {
		activityOwnerRun(t, e, target, "SELECT v FROM ssl_race_data", "SAVEPOINT keep", "ROLLBACK TO keep")
	}
	activityOwnerRun(t, e, admin, "REVOKE ssl_race_login FROM ssl_race_reader")
	await(denied)
	activityOwnerRun(t, e, admin, "GRANT ssl_race_login TO ssl_race_reader")
	await(restored)
	activityOwnerRun(t, e, target, "COMMIT")
	target.RequestEnd(session.CleanClose)
	if err := target.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	successor := sslCoreLogin(t, e, "ssl_race_login")
	activityOwnerRun(t, e, successor, "SELECT 37")
	if successor.SessionToken == target.SessionToken {
		t.Fatal("physical successor reused token")
	}
	stopWorker()
	if workerErr != nil || !reflect.DeepEqual(old, frozen) {
		t.Fatal("concurrent SSL capture/immutable old result", workerErr, old)
	}
	oldQuery := fmt.Sprintf("SELECT * FROM pg_stat_ssl WHERE pid=%d", oldPID)
	newQuery := fmt.Sprintf("SELECT * FROM pg_stat_ssl WHERE pid=%d", successor.SessionToken.ID())
	sslCoreWant(t, activityOwnerValues(t, e, monitor, oldQuery), uint64(oldPID), true)
	sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(successor.SessionToken.ID()), false)
	activityOwnerRun(t, e, monitor, "SELECT pg_stat_clear_snapshot()")
	sslCoreWant(t, activityOwnerValues(t, e, monitor, oldQuery), uint64(oldPID), false)
	sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(successor.SessionToken.ID()), true)
	activityOwnerRun(t, e, monitor, "ROLLBACK", "RESET ROLE")
	if !reflect.DeepEqual(old, frozen) {
		t.Fatal("clear/end changed old producer output")
	}
}
