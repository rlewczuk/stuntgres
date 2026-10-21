package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// driverRun is a synthetic run: it records every host call and hook step in
// the order the driver made them.
type driverRun struct {
	ran   []string
	index int
}

func (r *driverRun) record(format string, args ...any) {
	r.ran = append(r.ran, fmt.Sprintf(format, args...))
}

// driverHost is a fake Host that records into the run it serves.
type driverHost struct {
	run        *driverRun
	created    ids.ObjectRef
	startErr   error
	executeErr error
	publishErr error
}

func (h *driverHost) Begin(source string, statements []parser.Statement) error {
	h.run.record("begin %d", len(statements))
	return nil
}

func (h *driverHost) Start(context.Context) (*session.Operation, func(), error) {
	h.run.record("start")
	if h.startErr != nil {
		return nil, nil, h.startErr
	}
	return nil, func() { h.run.record("end") }, nil
}

func (h *driverHost) Statement(index int) {
	h.run.index = index
	h.run.record("statement %d", index)
}

func (h *driverHost) Execute(*session.Operation, parser.Statement) (Outcome, error) {
	h.run.record("execute %d", h.run.index)
	return Outcome{Created: h.created}, h.executeErr
}

func (h *driverHost) Publish(context.Context, *session.Operation) error {
	h.run.record("publish")
	return h.publishErr
}

// driverHooks is one entry for every phase. Statement 1 is consumed before
// admission and statement 2 by admission; afterErr fails the after step.
func driverHooks(t *testing.T, want Outcome, afterErr error) []Hook[*driverRun] {
	return []Hook[*driverRun]{{
		Name: "every phase",
		Decode: func(r *driverRun, comment string, _ parser.Position) (bool, error) {
			r.record("decode %s", strings.TrimSpace(strings.TrimPrefix(comment, "--")))
			return true, nil
		},
		Before: func(r *driverRun, _ *session.Operation, _ parser.Statement) (bool, error) {
			r.record("before %d", r.index)
			return r.index == 1, nil
		},
		Admit: func(r *driverRun, _ *session.Operation, _ parser.Statement) (Admission, error) {
			r.record("admit %d", r.index)
			if r.index == 2 {
				return Consumed, nil
			}
			return Admitted, nil
		},
		Prepare: func(r *driverRun, _ *session.Operation, _ parser.Statement) error {
			r.record("prepare %d", r.index)
			return nil
		},
		After: func(r *driverRun, _ *session.Operation, _ parser.Statement, outcome Outcome) error {
			if outcome != want {
				t.Errorf("after saw outcome %v, want %v", outcome, want)
			}
			r.record("after %d", r.index)
			return afterErr
		},
		Finish: func(r *driverRun, _ context.Context, _ *session.Operation) error {
			r.record("finish")
			return nil
		},
	}}
}

func assertDriverTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("driver ran\n  %s\nwant\n  %s", strings.Join(got, "; "), strings.Join(want, "; "))
	}
}

const driverScript = "SELECT 1;\n-- record\nSELECT 2;\nSELECT 3;\n"

func TestRestoreDriverOrder(t *testing.T) {
	run := new(driverRun)
	created := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}}
	host := &driverHost{run: run, created: created}
	if err := Restore(context.Background(), host, driverHooks(t, Outcome{Created: created}, nil), run, strings.NewReader(driverScript)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	assertDriverTrace(t, run.ran,
		"decode record", "begin 3", "start",
		"statement 0", "before 0", "admit 0", "prepare 0", "execute 0", "after 0",
		"statement 1", "before 1",
		"statement 2", "before 2", "admit 2",
		"finish", "publish", "end")
}

func TestRestoreDriverFailureSkipsPublish(t *testing.T) {
	failure := errors.New("after failed")
	run := new(driverRun)
	host := &driverHost{run: run}
	err := Restore(context.Background(), host, driverHooks(t, Outcome{}, failure), run, strings.NewReader(driverScript))
	if !errors.Is(err, failure) {
		t.Fatalf("Restore error = %v, want %v", err, failure)
	}
	assertDriverTrace(t, run.ran,
		"decode record", "begin 3", "start",
		"statement 0", "before 0", "admit 0", "prepare 0", "execute 0", "after 0",
		"end")

	failure = errors.New("execute failed")
	run = new(driverRun)
	host = &driverHost{run: run, executeErr: failure}
	if err := Restore(context.Background(), host, driverHooks(t, Outcome{}, nil), run, strings.NewReader(driverScript)); !errors.Is(err, failure) {
		t.Fatalf("Restore error = %v, want %v", err, failure)
	}
	assertDriverTrace(t, run.ran,
		"decode record", "begin 3", "start",
		"statement 0", "before 0", "admit 0", "prepare 0", "execute 0",
		"end")

	failure = errors.New("publish failed")
	run = new(driverRun)
	host = &driverHost{run: run, publishErr: failure}
	if err := Restore(context.Background(), host, nil, run, strings.NewReader("")); !errors.Is(err, failure) {
		t.Fatalf("Restore error = %v, want %v", err, failure)
	}
	assertDriverTrace(t, run.ran, "begin 0", "start", "publish", "end")
}

func TestRestoreDriverRejectsUnhandledStatement(t *testing.T) {
	run := new(driverRun)
	host := &driverHost{run: run}
	err := Restore(context.Background(), host, []Hook[*driverRun]{{Name: "no admission"}}, run, strings.NewReader("SELECT 1;"))
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Code != "0A000" || state.Message != rejection {
		t.Fatalf("Restore error = %v, want 0A000 %q", err, rejection)
	}
	assertDriverTrace(t, run.ran, "begin 1", "start", "statement 0", "end")
}

func TestRestoreDriverStopsBeforeStart(t *testing.T) {
	failure := errors.New("start failed")
	run := new(driverRun)
	if err := Restore(context.Background(), &driverHost{run: run, startErr: failure}, nil, run, strings.NewReader("SELECT 1;")); !errors.Is(err, failure) {
		t.Fatalf("Restore error = %v, want %v", err, failure)
	}
	assertDriverTrace(t, run.ran, "begin 1", "start")

	run = new(driverRun)
	if err := Restore(context.Background(), &driverHost{run: run}, nil, run, strings.NewReader("SELECT (;")); err == nil {
		t.Fatal("Restore accepted a malformed script")
	}
	assertDriverTrace(t, run.ran)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run = new(driverRun)
	if err := Restore(ctx, &driverHost{run: run}, nil, run, strings.NewReader("SELECT 1;")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Restore error = %v, want %v", err, context.Canceled)
	}
	assertDriverTrace(t, run.ran)
}
