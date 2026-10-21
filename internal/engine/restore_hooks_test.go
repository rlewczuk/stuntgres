package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// restoreHookTrace builds synthetic hooks that record the order they ran in.
type restoreHookTrace struct{ ran []string }

func (t *restoreHookTrace) before(name string, consumed bool, err error) restoreHook {
	return restoreHook{name: name, before: func(*restoreRun, *session.Operation, parser.Statement) (bool, error) {
		t.ran = append(t.ran, name)
		return consumed, err
	}}
}

func (t *restoreHookTrace) admit(name string, answer restoreAdmission, err error) restoreHook {
	return restoreHook{name: name, admit: func(*restoreRun, *session.Operation, parser.Statement) (restoreAdmission, error) {
		t.ran = append(t.ran, name)
		return answer, err
	}}
}

func (t *restoreHookTrace) step(name string, err error) restoreHook {
	return restoreHook{
		name: name,
		prepare: func(*restoreRun, *session.Operation, parser.Statement) error {
			t.ran = append(t.ran, "prepare "+name)
			return err
		},
		after: func(*restoreRun, *session.Operation, parser.Statement, Result) error {
			t.ran = append(t.ran, "after "+name)
			return err
		},
		finish: func(*restoreRun, context.Context, *session.Operation) error {
			t.ran = append(t.ran, "finish "+name)
			return err
		},
	}
}

func (t *restoreHookTrace) decode(name string, recognized bool, err error) restoreHook {
	return restoreHook{name: name, decode: func(_ *restoreRun, comment string, _ parser.Position) (bool, error) {
		t.ran = append(t.ran, name+":"+comment)
		return recognized, err
	}}
}

func assertRestoreTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("hooks ran %q, want %q", got, want)
	}
}

func TestRestoreHooksBeforeStopsAtConsumeOrError(t *testing.T) {
	failure := errors.New("before failed")
	var trace restoreHookTrace
	consumed, err := new(restoreRun).runBefore([]restoreHook{
		trace.before("first", false, nil),
		{name: "admit only", admit: func(*restoreRun, *session.Operation, parser.Statement) (restoreAdmission, error) {
			t.Fatal("before walk ran an admit hook")
			return restoreNotHandled, nil
		}},
		trace.before("consumer", true, nil),
		trace.before("unreached", false, nil),
	}, nil, nil)
	if !consumed || err != nil {
		t.Fatalf("runBefore = %v, %v; want consumed", consumed, err)
	}
	assertRestoreTrace(t, trace.ran, "first", "consumer")

	trace = restoreHookTrace{}
	consumed, err = new(restoreRun).runBefore([]restoreHook{
		trace.before("failing", false, failure),
		trace.before("unreached", false, nil),
	}, nil, nil)
	if consumed || !errors.Is(err, failure) {
		t.Fatalf("runBefore = %v, %v; want %v", consumed, err, failure)
	}
	assertRestoreTrace(t, trace.ran, "failing")
}

func TestRestoreHooksAdmitFirstAnswerWins(t *testing.T) {
	var trace restoreHookTrace
	answer, err := new(restoreRun).runAdmit([]restoreHook{
		trace.admit("other kind", restoreNotHandled, nil),
		trace.admit("repair", restoreConsumed, nil),
		trace.admit("unreached", restoreAdmitted, nil),
	}, nil, nil)
	if answer != restoreConsumed || err != nil {
		t.Fatalf("runAdmit = %v, %v; want consumed", answer, err)
	}
	assertRestoreTrace(t, trace.ran, "other kind", "repair")

	failure := errors.New("admission failed")
	trace = restoreHookTrace{}
	_, err = new(restoreRun).runAdmit([]restoreHook{
		trace.admit("failing", restoreNotHandled, failure),
		trace.admit("unreached", restoreAdmitted, nil),
	}, nil, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("runAdmit error = %v, want %v", err, failure)
	}
	assertRestoreTrace(t, trace.ran, "failing")
}

func TestRestoreHooksAdmitRejectsUnhandledStatement(t *testing.T) {
	var trace restoreHookTrace
	answer, err := new(restoreRun).runAdmit([]restoreHook{
		trace.admit("other kind", restoreNotHandled, nil),
	}, nil, nil)
	var state *sqlstate.Error
	if answer != restoreNotHandled || !errors.As(err, &state) || state.Code != "0A000" || state.Message != restoreRejection {
		t.Fatalf("runAdmit = %v, %v; want 0A000 %q", answer, err, restoreRejection)
	}
	assertRestoreTrace(t, trace.ran, "other kind")
}

func TestRestoreHooksStepsStopAtFirstError(t *testing.T) {
	failure := errors.New("step failed")
	var trace restoreHookTrace
	hooks := []restoreHook{trace.step("first", nil), trace.step("failing", failure), trace.step("unreached", nil)}
	run := new(restoreRun)
	if err := run.runPrepare(hooks, nil, nil); !errors.Is(err, failure) {
		t.Fatalf("runPrepare error = %v, want %v", err, failure)
	}
	if err := run.runAfter(hooks, nil, nil, Result{}); !errors.Is(err, failure) {
		t.Fatalf("runAfter error = %v, want %v", err, failure)
	}
	if err := run.runFinish(context.Background(), hooks, nil); !errors.Is(err, failure) {
		t.Fatalf("runFinish error = %v, want %v", err, failure)
	}
	assertRestoreTrace(t, trace.ran,
		"prepare first", "prepare failing",
		"after first", "after failing",
		"finish first", "finish failing")
}

func TestRestoreHooksDecodeFirstRecognizedWins(t *testing.T) {
	var trace restoreHookTrace
	hooks := []restoreHook{
		trace.decode("domain", false, nil),
		trace.decode("owners", true, nil),
		trace.decode("snapshot", true, nil),
	}
	if err := new(restoreRun).decodeComment(hooks, "c", parser.Position{}); err != nil {
		t.Fatalf("decodeComment: %v", err)
	}
	assertRestoreTrace(t, trace.ran, "domain:c", "owners:c")

	trace = restoreHookTrace{}
	if err := new(restoreRun).decodeComment([]restoreHook{trace.decode("domain", false, nil)}, "plain", parser.Position{}); err != nil {
		t.Fatalf("unrecognized comment: %v", err)
	}

	failure := errors.New("malformed record")
	trace = restoreHookTrace{}
	err := new(restoreRun).decodeComment([]restoreHook{
		trace.decode("domain", false, failure),
		trace.decode("unreached", true, nil),
	}, "bad", parser.Position{})
	if !errors.Is(err, failure) {
		t.Fatalf("decodeComment error = %v, want %v", err, failure)
	}
	assertRestoreTrace(t, trace.ran, "domain:bad")
}

// A field step in an INSERT target is not a subscript; it neither consumes
// the statement nor stops the remaining targets' subscript validation.
func TestRestoreAdmitsInsertTargetIndirection(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want restoreAdmission
		fail bool
	}{
		{sql: "INSERT INTO t (c.f, a[1]) VALUES (1, 2)", want: restoreAdmitted},
		{sql: "INSERT INTO t (c.f, a[$1]) VALUES (1, 2)", fail: true},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.sql, err)
		}
		answer, err := new(restoreRun).runAdmit(restoreHooks, nil, statements[0])
		if tc.fail {
			if err == nil {
				t.Fatalf("%q admitted as %v, want an error", tc.sql, answer)
			}
			continue
		}
		if err != nil || answer != tc.want {
			t.Fatalf("%q = %v, %v; want %v", tc.sql, answer, err, tc.want)
		}
	}
}
