package control

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// hookRun is a synthetic run for the walker tests.
type hookRun struct{}

// hookTrace builds synthetic hooks that record the order they ran in.
type hookTrace struct{ ran []string }

func (t *hookTrace) before(name string, consumed bool, err error) Hook[*hookRun] {
	return Hook[*hookRun]{Name: name, Before: func(*hookRun, *session.Operation, parser.Statement) (bool, error) {
		t.ran = append(t.ran, name)
		return consumed, err
	}}
}

func (t *hookTrace) admit(name string, answer Admission, err error) Hook[*hookRun] {
	return Hook[*hookRun]{Name: name, Admit: func(*hookRun, *session.Operation, parser.Statement) (Admission, error) {
		t.ran = append(t.ran, name)
		return answer, err
	}}
}

func (t *hookTrace) step(name string, err error) Hook[*hookRun] {
	return Hook[*hookRun]{
		Name: name,
		Prepare: func(*hookRun, *session.Operation, parser.Statement) error {
			t.ran = append(t.ran, "prepare "+name)
			return err
		},
		After: func(*hookRun, *session.Operation, parser.Statement, Outcome) error {
			t.ran = append(t.ran, "after "+name)
			return err
		},
		Finish: func(*hookRun, context.Context, *session.Operation) error {
			t.ran = append(t.ran, "finish "+name)
			return err
		},
	}
}

func (t *hookTrace) decode(name string, recognized bool, err error) Hook[*hookRun] {
	return Hook[*hookRun]{Name: name, Decode: func(_ *hookRun, comment string, _ parser.Position) (bool, error) {
		t.ran = append(t.ran, name+":"+comment)
		return recognized, err
	}}
}

func assertHookTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("hooks ran %q, want %q", got, want)
	}
}

func TestRestoreHooksBeforeStopsAtConsumeOrError(t *testing.T) {
	failure := errors.New("before failed")
	var trace hookTrace
	consumed, err := runBefore([]Hook[*hookRun]{
		trace.before("first", false, nil),
		{Name: "admit only", Admit: func(*hookRun, *session.Operation, parser.Statement) (Admission, error) {
			t.Fatal("before walk ran an admit hook")
			return NotHandled, nil
		}},
		trace.before("consumer", true, nil),
		trace.before("unreached", false, nil),
	}, new(hookRun), nil, nil)
	if !consumed || err != nil {
		t.Fatalf("runBefore = %v, %v; want consumed", consumed, err)
	}
	assertHookTrace(t, trace.ran, "first", "consumer")

	trace = hookTrace{}
	consumed, err = runBefore([]Hook[*hookRun]{
		trace.before("failing", false, failure),
		trace.before("unreached", false, nil),
	}, new(hookRun), nil, nil)
	if consumed || !errors.Is(err, failure) {
		t.Fatalf("runBefore = %v, %v; want %v", consumed, err, failure)
	}
	assertHookTrace(t, trace.ran, "failing")
}

func TestRestoreHooksAdmitFirstAnswerWins(t *testing.T) {
	var trace hookTrace
	answer, err := runAdmit([]Hook[*hookRun]{
		trace.admit("other kind", NotHandled, nil),
		trace.admit("repair", Consumed, nil),
		trace.admit("unreached", Admitted, nil),
	}, new(hookRun), nil, nil)
	if answer != Consumed || err != nil {
		t.Fatalf("runAdmit = %v, %v; want consumed", answer, err)
	}
	assertHookTrace(t, trace.ran, "other kind", "repair")

	failure := errors.New("admission failed")
	trace = hookTrace{}
	_, err = runAdmit([]Hook[*hookRun]{
		trace.admit("failing", NotHandled, failure),
		trace.admit("unreached", Admitted, nil),
	}, new(hookRun), nil, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("runAdmit error = %v, want %v", err, failure)
	}
	assertHookTrace(t, trace.ran, "failing")
}

func TestRestoreHooksAdmitRejectsUnhandledStatement(t *testing.T) {
	var trace hookTrace
	answer, err := runAdmit([]Hook[*hookRun]{
		trace.admit("other kind", NotHandled, nil),
	}, new(hookRun), nil, nil)
	var state *sqlstate.Error
	if answer != NotHandled || !errors.As(err, &state) || state.Code != "0A000" || state.Message != rejection {
		t.Fatalf("runAdmit = %v, %v; want 0A000 %q", answer, err, rejection)
	}
	assertHookTrace(t, trace.ran, "other kind")
}

func TestRestoreHooksStepsStopAtFirstError(t *testing.T) {
	failure := errors.New("step failed")
	var trace hookTrace
	hooks := []Hook[*hookRun]{trace.step("first", nil), trace.step("failing", failure), trace.step("unreached", nil)}
	run := new(hookRun)
	if err := runPrepare(hooks, run, nil, nil); !errors.Is(err, failure) {
		t.Fatalf("runPrepare error = %v, want %v", err, failure)
	}
	if err := runAfter(hooks, run, nil, nil, Outcome{}); !errors.Is(err, failure) {
		t.Fatalf("runAfter error = %v, want %v", err, failure)
	}
	if err := runFinish(context.Background(), hooks, run, nil); !errors.Is(err, failure) {
		t.Fatalf("runFinish error = %v, want %v", err, failure)
	}
	assertHookTrace(t, trace.ran,
		"prepare first", "prepare failing",
		"after first", "after failing",
		"finish first", "finish failing")
}

func TestRestoreHooksDecodeFirstRecognizedWins(t *testing.T) {
	var trace hookTrace
	hooks := []Hook[*hookRun]{
		trace.decode("domain", false, nil),
		trace.decode("owners", true, nil),
		trace.decode("snapshot", true, nil),
	}
	if err := decodeComment(hooks, new(hookRun), "c", parser.Position{}); err != nil {
		t.Fatalf("decodeComment: %v", err)
	}
	assertHookTrace(t, trace.ran, "domain:c", "owners:c")

	trace = hookTrace{}
	if err := decodeComment([]Hook[*hookRun]{trace.decode("domain", false, nil)}, new(hookRun), "plain", parser.Position{}); err != nil {
		t.Fatalf("unrecognized comment: %v", err)
	}

	failure := errors.New("malformed record")
	trace = hookTrace{}
	err := decodeComment([]Hook[*hookRun]{
		trace.decode("domain", false, failure),
		trace.decode("unreached", true, nil),
	}, new(hookRun), "bad", parser.Position{})
	if !errors.Is(err, failure) {
		t.Fatalf("decodeComment error = %v, want %v", err, failure)
	}
	assertHookTrace(t, trace.ran, "domain:bad")
}
