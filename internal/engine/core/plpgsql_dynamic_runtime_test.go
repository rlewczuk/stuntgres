package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLDynamicSQLRuntime(t *testing.T) {
	for _, command := range []string{"SELECT $1::integer", "SELEC 1", "SELECT absent", "SELECT 1/$1::integer"} {
		t.Run(command, func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, "DECLARE x integer; BEGIN EXECUTE '"+command+"' INTO x USING 0; RETURN 7; END")
			body := d.SQL.PL.Root.Statements[0].Block
			f.Enter(d.SQL.PL.Root)
			defer f.Leave(d.SQL.PL.Root)
			f.Enter(body)
			defer f.Leave(body)
			run := core.ScopeNewPLRun(s, d, f, read)
			expected, completed, depth, demand := s.Expected, s.Completed, core.ScopeActiveSQLRoutineDepth(s), core.ScopeSQLRoutine(s)
			for range 2 {
				err := run.Host.Dynamic(run, f, body.Statements[0])
				if (command == "SELECT $1::integer") != (err == nil) {
					t.Fatal("reached command", err)
				}
				if s.Expected != expected || s.Completed != completed || core.ScopeActiveSQLRoutineDepth(s) != depth || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), demand) || d.SQL.PL.ValidateDefinition(d.SQL) != nil {
					t.Fatal("dynamic activation escaped", err)
				}
			}
		})
	}
	t.Run("reducer_cleanup_and_cancel", func(t *testing.T) {
		s, _, frame, read := loopUnitFrame(t, "BEGIN RETURN 7; END")
		integer, _ := read.LookupType(sqltype.Int4)
		one, _ := sqltype.Integer(integer, 1)
		two, _ := sqltype.Integer(integer, 2)
		three, _ := sqltype.Integer(integer, 3)
		schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
		late := &sqlstate.Error{Code: "22012", Message: "division by zero"}
		for _, fail := range []bool{false, true} {
			evaluated := 0
			projection := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				evaluated++
				if fail && evaluated == 3 {
					return sqltype.Value{}, late
				}
				return (plan.ColumnExpr{Info: exprInfo(integer.Usage()), Scope: 1, Frame: 1, Slot: 0}).Eval(c)
			})
			producer, _ := queryLoopUnitRows(t, schema, [][]sqltype.Value{{one}, {two}, {three}}, projection)
			before := core.ScopeSQLRoutine(s)
			restore := s.ActivateSQLRoutine(nil, read)
			core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(s), frame)
			closedInActivation := false
			cleanup := errors.New("dynamic close")
			rows := &sqlExecutionCloseRows{RowResult: producer, err: cleanup, before: func() { closedInActivation = core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) == frame }}
			first, count, processed, err := core.DrainPLDynamic(context.Background(), rows)
			wantProcessed := int64(3)
			if fail {
				wantProcessed = 2
			}
			if evaluated != 3 || count != 2 || processed != wantProcessed || len(first) != 1 || first[0] != one || fail && !errors.Is(err, late) || !fail && err != nil {
				t.Fatal("incomplete dynamic drain", evaluated, count, processed, first, err)
			}
			err = restore(core.ClosePLCapture(core.Result{Rows: rows}, err, func(e error) error { return e }))
			var fatal *proc.PLExceptionCleanupError
			if !errors.Is(err, cleanup) || fail && !errors.Is(err, late) || !errors.As(err, &fatal) || rows.count != 1 || !closedInActivation || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), before) {
				t.Fatal("cleanup identity/order", err)
			}
		}
		producer, _ := queryLoopUnitRows(t, schema, nil, nil)
		first, count, processed, err := core.DrainPLDynamic(context.Background(), producer)
		if err != nil || count != 0 || processed != 0 || len(first) != 1 || first[0].Type() != integer || !first[0].IsNull() {
			t.Fatal("unshaped empty result", first, count, processed, err)
		}
		producer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		admitted, release, joined := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		projection := queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { close(admitted); <-release; return one, nil })
		producer, _ = queryLoopUnitRows(t, schema, [][]sqltype.Value{{one}, {two}}, projection)
		go func() {
			_, _, _, err := core.DrainPLDynamic(ctx, producer)
			joined <- core.ClosePLCapture(core.Result{Rows: producer}, err, func(e error) error { return e })
		}()
		<-admitted
		cancel()
		close(release)
		if err := <-joined; !errors.Is(err, context.Canceled) || !producer.closed {
			t.Fatal("unjoined dynamic cancellation", err)
		}
	})
}

func TestPLpgSQLDynamicSQLSetSink(t *testing.T) {
	s, d, _, read := loopUnitFrame(t, "BEGIN RETURN 7; END")
	for _, source := range []string{"BEGIN RETURN; END", "BEGIN END", "BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer' USING 1; RETURN QUERY EXECUTE 'SELECT 2::integer'; END"} {
		d.SQL.Source = source
		d.SQL.ReturnsSet = true
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		d.SQL.PLSyntax = raw
		s.Engine().Mu.Lock()
		auth, err := s.Engine().AuthorizationLocked(s.Op)
		if err == nil {
			err = s.Engine().CompilePLRoutineLocked(s.Op, auth, d)
		}
		s.Engine().Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		input := proc.NewFrame(nil)
		var first, second []sqltype.Value
		_, err = core.ScopeInvokePLRoutineSet(s, d, input, read, sqltype.CollationRef{}, &first)
		if err != nil {
			t.Fatal(err)
		}
		_, err = core.ScopeInvokePLRoutineSet(s, d, input, read, sqltype.CollationRef{}, &second)
		input.Close()
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Fatal("set invocation", err)
		}
		if source == "BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer' USING 1; RETURN QUERY EXECUTE 'SELECT 2::integer'; END" {
			if len(first) != 2 || &first[0] == &second[0] {
				t.Fatal("shared/missing set sink")
			}
			for i, value := range first {
				n, e := value.Integer()
				if e != nil || n != int64(i+1) {
					t.Fatal("append result", value, e)
				}
			}
		} else if len(first) != 0 {
			t.Fatal("empty invocation appended scalar NULL")
		}
	}
}
