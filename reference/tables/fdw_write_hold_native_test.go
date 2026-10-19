package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWriteHeldNativeCommit(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader) {
	t.Helper()
	pool := fdwRecordedPool(t, p.NativeConnector)
	defer pool.Close()
	conn, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	runTx := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, step) }
	for _, name := range []string{"fdw_native_hold_a", "fdw_native_hold_b"} {
		runTx(fixture.Exec("DECLARE "+name+" NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
	}
	runTx(fdwRows("FETCH 1 FROM fdw_native_hold_a", [][]any{{int64(1), "one", true}}))
	begins, ends := r.writer.statementBegins, r.writer.statementEnds
	var events []string
	r.writer.writeHook = func(ctx context.Context, name string) error {
		switch name {
		case "EndForeignScan", "EndStatement", "Commit":
			events = append(events, name)
		}
		return nil
	}
	err = tx.Commit()
	r.writer.writeHook = nil
	if err != nil {
		t.Fatal(err)
	}
	if r.writer.statementBegins-begins != 1 || r.writer.statementEnds-ends != 1 || !reflect.DeepEqual(events, []string{"EndForeignScan", "EndForeignScan", "EndStatement", "Commit"}) {
		t.Fatal("native HOLD mark/resource ownership", events, r.writer.statementBegins-begins, r.writer.statementEnds-ends)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fdwRows("FETCH 1 FROM fdw_native_hold_a", [][]any{{int64(2), nil, false}}))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fdwRows("FETCH 1 FROM fdw_native_hold_b", [][]any{{int64(1), "one", true}}))
	for _, name := range []string{"fdw_native_hold_a", "fdw_native_hold_b"} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CLOSE "+name, 0))
	}
}

func fdwWriteHeldFaults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	for _, phase := range []string{"IterateForeignScan", "EndForeignScan", "EndStatement", "Commit", "Cancel"} {
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_local_commit VALUES(64)", 1))
		for _, name := range []string{"fdw_hold_fault_a", "fdw_hold_fault_b"} {
			run(fixture.Exec("DECLARE "+name+" NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		failure := errors.New("participating HOLD " + phase + " failed")
		want := error(failure)
		if phase == "Cancel" {
			want = context.Canceled
		}
		activeScan := 0
		begins, ends, scanEnds := r.writer.statementBegins, r.writer.statementEnds, r.writer.ends
		r.writer.writeHook = func(ctx context.Context, name string) error {
			if name == "BeginForeignScan" {
				activeScan++
			}
			if phase == "Cancel" && activeScan == 2 && name == "IterateForeignScan" {
				cancel()
				return ctx.Err()
			}
			if name == phase && (phase == "EndStatement" || phase == "Commit" || activeScan == 2) {
				return failure
			}
			return nil
		}
		fdwWriteFailedExec(t, ctx, c, "COMMIT", want)
		r.writer.writeHook = nil
		cancel()
		if r.writer.statementBegins-begins != 1 || r.writer.statementEnds-ends != 1 || r.writer.ends-scanEnds != 2 || r.snapshot().openPools != 0 {
			t.Fatal("HOLD failure leaked/replayed participant resources", phase, r.writer.statementBegins-begins, r.writer.statementEnds-ends, r.writer.ends-scanEnds, r.snapshot())
		}
		for _, name := range []string{"fdw_hold_fault_a", "fdw_hold_fault_b"} {
			run(fixture.ErrorDiagnostic("FETCH 1 FROM "+name, fdwDiagnostic("34000", "cursor \""+name+"\" does not exist")))
		}
		run(fixture.Select("SELECT id FROM fdw_local_commit", []string{"id"}, []string{"INT4"}, nil))
	}
	for _, phase := range []string{"EndStatement", "Commit"} {
		failure := errors.New("implicit HOLD " + phase + " failed")
		r.writer.writeHook = func(ctx context.Context, name string) error {
			if name == phase {
				return failure
			}
			return nil
		}
		fdwWriteFailedExec(t, p.Ctx, c, "DECLARE fdw_implicit_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", failure)
		r.writer.writeHook = nil
		run(fixture.ErrorDiagnostic("FETCH 1 FROM fdw_implicit_hold", fdwDiagnostic("34000", `cursor "fdw_implicit_hold" does not exist`)))
	}
	run(fixture.Exec("DECLARE fdw_implicit_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
	run(fdwRows("FETCH 1 FROM fdw_implicit_hold", [][]any{{int64(1), "one", true}}))
	run(fixture.Exec("CLOSE fdw_implicit_hold", 0))
	run(fixture.Exec("CREATE TABLE fdw_hold_preflight(id INT,CONSTRAINT fdw_hold_ex EXCLUDE USING btree(id WITH =) DEFERRABLE INITIALLY DEFERRED)", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_hold_preflight VALUES(1),(1)", 2))
	run(fixture.Exec("DECLARE fdw_preflight_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
	begins, ends := r.writer.statementBegins, r.writer.statementEnds
	var events []string
	r.writer.writeHook = func(ctx context.Context, name string) error {
		switch name {
		case "EndForeignScan", "EndStatement", "Commit":
			events = append(events, name)
		}
		return nil
	}
	_, err := c.ExecContext(p.Ctx, "COMMIT")
	r.writer.writeHook = nil
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
		t.Fatal("deferred publication preflight did not fail", err)
	}
	if r.writer.statementBegins-begins != 1 || r.writer.statementEnds-ends != 1 || !reflect.DeepEqual(events, []string{"EndForeignScan", "EndStatement"}) || r.snapshot().openPools != 0 {
		t.Fatal("preflight failure settled HOLD marks out of order", events, r.snapshot())
	}
	run(fixture.ErrorDiagnostic("FETCH 1 FROM fdw_preflight_hold", fdwDiagnostic("34000", `cursor "fdw_preflight_hold" does not exist`)))
	run(fixture.Select("SELECT id FROM fdw_hold_preflight", []string{"id"}, []string{"INT4"}, nil))
}
