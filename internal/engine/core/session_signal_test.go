package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestSessionSignalLifecycle(t *testing.T) {
	e := engine.New()
	caller := enginetest.StartSession(t, e, nil)
	op, _, err := caller.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.Mu.Lock()
	auth, err := scope.RoutineAuthorizationLocked()
	if err == nil {
		err = scope.CaptureIdentityLocked(auth)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope)}}
	result := enginetest.Usage(e, sqltype.Bool).Type
	databaseAdmissionCount(t, e, 1)
	victim := enginetest.StartSession(t, e, nil)
	for _, setup := range []string{"CREATE TEMP TABLE session_signal_owned(v integer)", "PREPARE session_signal_owned AS SELECT 42", "LISTEN session_signal_owned", "BEGIN", "DECLARE session_signal_owned CURSOR FOR SELECT v FROM session_signal_owned"} {
		enginetest.SessionSQL(t, victim, setup)
	}
	held, _, err := victim.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Done()
	databaseAdmissionCount(t, e, 2)
	pid := int64(victim.Token().ID())
	signal := func(timeout int64, want bool) {
		t.Helper()
		v, err := evaluation.SessionSignalValue(functions.SignalTerminate, pid, timeout, result)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Boolean()
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	signal(0, true)
	select {
	case <-victim.Completed:
		t.Fatal("sent success falsely implied lease drain")
	default:
	}
	for _, ms := range []int64{1, 2} {
		signal(ms, false)
		notices := op.DetachNotices()
		unit := "milliseconds"
		if ms == 1 {
			unit = "millisecond"
		}
		want := fmt.Sprintf("backend with PID %d did not terminate within %d %s", pid, ms, unit)
		if len(notices) != 1 || notices[0].Code != "01000" || notices[0].Severity != "WARNING" || notices[0].Message != want {
			t.Fatal(notices, want)
		}
	}
	signal(0, true) // Registered Ending is still addressable and idempotent.
	databaseAdmissionCount(t, e, 2)
	held.Done()
	if err := victim.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	victim.Mu.Lock()
	state, leases := victim.State, victim.Leases
	for _, owner := range victim.Resources {
		if owner != nil {
			t.Error("resource still owned after Completed")
		}
	}
	victim.Mu.Unlock()
	if state != session.Ended || leases != 0 {
		t.Fatal(state, leases)
	}
	databaseAdmissionCount(t, e, 1)
	signal(0, false)
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Message != fmt.Sprintf("PID %d is not a PostgreSQL backend process", pid) {
		t.Fatal(notices)
	}
	// Completion, rather than a cleanup error value, owns positive-wait success.
	ok, err := victim.WaitTermination(context.Background(), 1000)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}
