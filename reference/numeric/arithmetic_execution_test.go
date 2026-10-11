package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceArithmeticExecutionHeldFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,0,0)", 2))
	const query = "SELECT i+$1 FROM t WHERE id=1"
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, arg := range []any{2, nil, 3} {
				var want any
				if arg != nil {
					want = int64(arg.(int) + 7)
				}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"?column?"}, []string{"INT4"}, [][]any{{want}}, arg))
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			values := make([]int64, 8)
			errs := make([]error, 8)
			for n := range 8 {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					<-start
					ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
					errs[n] = stmt.QueryRowContext(ctx, n).Scan(&values[n])
				}(n)
			}
			close(start)
			wg.Wait()
			for n := range 8 {
				if errs[n] != nil || values[n] != int64(n+7) {
					t.Fatalf("frame %d value=%d error=%v", n, values[n], errs[n])
				}
			}
			backend.Recorder.Mu.Lock()
			matched := 0
			for _, op := range backend.Recorder.Operations {
				if op.SQL != query || op.Err != nil || op.Columns == nil {
					continue
				}
				if !op.Closed || op.CloseErr != nil {
					t.Errorf("frame cursor closed=%v error=%v", op.Closed, op.CloseErr)
				}
				matched++
				if len(op.Args) != 1 || len(op.Rows) != 1 || len(op.Rows[0]) != 1 || len(op.Columns) != 1 || op.Columns[0] != "?column?" {
					t.Errorf("held recorded frame args=%#v columns=%#v rows=%#v", op.Args, op.Columns, op.Rows)
					continue
				}
				var want any
				if op.Args[0].Value != nil {
					want = op.Args[0].Value.(int64) + 7
				}
				if op.Rows[0][0] != want {
					t.Errorf("held recorded value=%#v want=%#v", op.Rows[0][0], want)
				}
			}
			backend.Recorder.Mu.Unlock()
			if matched != 11 {
				t.Fatalf("consumed held frames %d want 11", matched)
			}
		})
	}
	step := fixture.Select("SELECT 7/i FROM t WHERE id=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	failure := fixture.ImplicitError("SELECT 7/i FROM t ORDER BY id", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	failure.SelectRows, failure.ErrorStage = true, "execute"
	p.Step(t, failure)
	p.Step(t, fixture.Prepared(failure))
}

func TestReferenceArithmeticExecutionTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,0,0)", 2))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			writer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := writer.Close(); err != nil {
					t.Error(err)
				}
			}()
			reader, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			}()
			fixture.RunStep(t, p.Ctx, writer, backend.Recorder, fixture.Exec("BEGIN", 0))
			bad := fixture.ImplicitError("INSERT INTO t VALUES(3,3+1,4),(4,7/0,4)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			bad.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{writer, t}, backend.Recorder, bad)
			fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Select("SELECT id FROM t WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Select("SELECT id FROM t WHERE id=3", []string{"id"}, []string{"INT4"}, nil))
			aborted := fixture.ImplicitError("INSERT INTO t VALUES(5,5,5)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			aborted.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{writer, t}, backend.Recorder, aborted)
			fixture.RunStep(t, p.Ctx, writer, backend.Recorder, fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, writer, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(5,5+1,5)", 1))
			fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Select("SELECT id,i,b FROM t WHERE id=5", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(5), int64(6), int64(5)}}))
		})
	}
}

func TestReferenceArithmeticExecutionCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,0,0)", 2))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT i+$1 FROM t WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.QueryContext(ctx, 1)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query exposed cursor")
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1+2)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cursorContext, stop := context.WithCancel(p.Ctx)
	defer stop()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(cursorContext, "SELECT i+1 FROM t ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		stop()
		dst := make([]driver.Value, 1)
		for range 2 {
			if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
				t.Fatalf("repeated canceled Next: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const script = "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(/*é*/(7+$1)*2)"
	position := len([]rune(script[:strings.Index(script, "$1")])) + 1
	want := fixture.ImplicitError(script, fixture.Diagnostic{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(position)})
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), want.Diagnostic)
	step := fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(0), int64(0)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}
