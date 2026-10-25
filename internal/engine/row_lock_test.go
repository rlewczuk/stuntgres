package engine

import (
	"database/sql/driver"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

var rowLockTestClauses = []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"}

func rowLockEngineFixture(t *testing.T) (*Engine, *session.Session, *session.Session, *session.Session) {
	t.Helper()
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, c, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, c, "INSERT INTO t VALUES(1,10),(2,20)")
	return e, a, b, c
}

func rowLockQueued(t *testing.T, e *Engine, holder *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		found := false
		for resource, entry := range lockState.Resources {
			if resource.Row == 0 || entry.Holders[holder].Mode == 0 {
				continue
			}
			for _, request := range entry.Pending {
				if request.Mode == mode && !request.Terminal && request.Ticket != holder {
					found = true
				}
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("exact row request not queued")
		}
		runtime.Gosched()
	}
}

func rowLockAwait(t *testing.T, done <-chan deleteOutcome, want ...int64) {
	t.Helper()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Rows == nil {
			t.Fatal("locker returned no cursor")
		}
		defer outcome.result.Rows.Close()
		got := tuples(outcome.result)
		expected := make([][]driver.Value, len(want))
		for i, v := range want {
			expected[i] = []driver.Value{v}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("rows %v want %v", got, expected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("locker failed to finish")
	}
}
