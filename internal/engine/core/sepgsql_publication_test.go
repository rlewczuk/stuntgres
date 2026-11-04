package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

func sepgsqlPhaseEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "phase-owner", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "P", Class: "db_database", Allow: []string{"access", "getattr"}},
			{Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search", "add_name", "remove_name", "getattr"}},
			{Source: "R", Target: "P", Class: "db_table", Allow: []string{"select", "insert", "update", "delete", "lock", "getattr", "setattr", "drop", "create"}},
			{Source: "R", Target: "P", Class: "db_column", Allow: []string{"select", "insert", "update", "getattr", "setattr", "drop", "create"}},
			{Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute", "getattr"}},
			{Source: "R", Target: "S", Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
			{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
			{Source: "T", Target: "P", Class: "db_procedure", Allow: []string{"execute"}},
			{Source: "T", Target: "U", Class: "db_procedure", Allow: []string{"execute"}},
		}, TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(policy, true)
	if err == nil {
		e.InitializeSepgsqlSeedLabels()
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE TABLE sepgsql_phase_target(v integer)")
	enginetest.Run(t, e, "CREATE INDEX sepgsql_phase_idx ON sepgsql_phase_target(v)")
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.round(numeric,integer) IS 'U'")
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.round(numeric) IS 'S'")
	return e
}

func sepgsqlPhaseRows(t *testing.T, e *core.Engine, op *session.Operation, sql string, want [][]driver.Value) {
	t.Helper()
	result := enginetest.RoleExecute(t, e, op, sql)
	got := enginetest.Tuples(result)
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", sql, got, want)
	}
}

// The genuine outside-lock index merge is an existing publication seam. Stack
// identification selects that boundary, not an incidental Err call count. This
// adds no production test hook, timer race, or modeled allow decision.
type sepgsqlMergeContext struct {
	context.Context
	entered, release chan struct{}
	paused           atomic.Bool
}

func (c *sepgsqlMergeContext) Err() error {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	merge := false
	for {
		frame, more := frames.Next()
		merge = merge || strings.HasSuffix(frame.Function, ".MergeIndexRoot")
		if !more {
			break
		}
	}
	if merge && c.paused.CompareAndSwap(false, true) {
		close(c.entered)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}

func TestSepgsqlSimulationPublication(t *testing.T) {
	t.Run("entered_body_returned_caller", func(t *testing.T) {
		e := sepgsqlPhaseEngine(t)
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		// The selected INTERNAL inner round is U, executable only by T. The real
		// outer SQL wrapper enters T, returns to R, and the final indexed mutation
		// publishes under the actual historical decisions, not an R replay of U.
		enginetest.RoleExecute(t, e, op, "INSERT INTO sepgsql_phase_target SELECT (round(3.14::numeric))::integer")
		sepgsqlPhaseRows(t, e, op, "SELECT v FROM sepgsql_phase_target", [][]driver.Value{{int64(3)}})
		sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		for _, sql := range []string{
			"SELECT round(3.14::numeric,0)",
			"SELECT round(3.14::numeric),round(3.14::numeric,0)",
			"SELECT round(3.14::numeric,0),round(3.14::numeric)",
		} {
			enginetest.RoleError(t, e, op, sql, "42501", "SELinux: security policy violation", "")
		}
		sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		enginetest.RoleExecute(t, e, op, "INSERT INTO sepgsql_phase_target SELECT (round(8.6::numeric))::integer")
		sepgsqlPhaseRows(t, e, op, "SELECT v FROM sepgsql_phase_target ORDER BY v", [][]driver.Value{{int64(3)}, {int64(9)}})
	})
	t.Run("actual_peer_inner_label_changes_before_final_fence", func(t *testing.T) {
		e := sepgsqlPhaseEngine(t)
		writer := enginetest.StartSession(t, e, nil)
		peer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, writer, "SELECT sepgsql_setcon('R')")
		statement := enginetest.Statement(t, "INSERT INTO sepgsql_phase_target SELECT (round(3.14::numeric))::integer")
		base, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		op, err := writer.Acquire(base)
		if err != nil {
			t.Fatal(err)
		}
		barrier := &sepgsqlMergeContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
		op.Ctx = barrier
		finished := make(chan error, 1)
		var release sync.Once
		joined := false
		t.Cleanup(func() {
			cancel()
			release.Do(func() { close(barrier.release) })
			if !joined {
				<-finished
			}
			op.Done()
		})
		go func() {
			result, err := e.Execute(op, statement, 0, nil)
			if result.Rows != nil {
				if closeErr := result.Rows.Close(); err == nil {
					err = closeErr
				}
			}
			finished <- err
		}()
		select {
		case <-barrier.entered:
		case err := <-finished:
			joined = true
			t.Fatalf("real merge boundary not reached: %v", err)
		case <-base.Done():
			t.Fatal("real merge boundary timed out")
		}
		// This is a genuine independent physical session and label publication,
		// not a mocked root replacement or mutation of an observation's fields.
		enginetest.SessionSQL(t, peer, "SECURITY LABEL ON FUNCTION pg_catalog.round(numeric,integer) IS 'P'")
		release.Do(func() { close(barrier.release) })
		err = <-finished
		joined = true
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatalf("stale actual inner-label decision published: %T %v", err, err)
		}
		sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		check, finish := enginetest.RoleOperation(t, e, "postgres")
		defer finish()
		sepgsqlPhaseRows(t, e, check, "SELECT count(*) FROM sepgsql_phase_target", [][]driver.Value{{int64(0)}})
		sepgsqlPhaseRows(t, e, check, "SELECT label FROM pg_seclabel WHERE objoid='round(numeric,integer)'::regprocedure", [][]driver.Value{{"P"}})
	})
}
