package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type captureDemandRows struct {
	values  [][]sqltype.Value
	at      int
	failAt  int
	failure error
	cancel  context.CancelFunc
}

func (r *captureDemandRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.at++
	if r.failAt == r.at {
		return false, r.failure
	}
	if r.at > len(r.values) {
		return false, nil
	}
	copy(dst, r.values[r.at-1])
	if r.cancel != nil {
		r.cancel()
	}
	return true, nil
}

type captureCloseRows struct {
	exec.RowResult
	failure error
	closed  bool
}

func (r *captureCloseRows) Close() error { r.closed = true; return r.failure }
func TestPLpgSQLSelectIntoRuntime(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	if integer == nil {
		t.Fatal("missing integer")
	}
	columns := core.PLColumns([]catalog.Column{{Name: "i", Usage: integer.Usage()}})
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []bool{false, true} {
		for count := 0; count <= 3; count++ {
			values := [][]sqltype.Value{{one}, {two}, {one}}
			rows := &captureDemandRows{values: values[:count]}
			fields, present, many, err := core.DemandPLCapture(context.Background(), rows, columns, exact)
			if err != nil || present != (count > 0) || many != (exact && count > 1) {
				t.Fatal("cardinality", count, exact, present, many, err)
			}
			if count == 0 && fields[0] != sqltype.Null(integer) || count > 0 && fields[0] != one {
				t.Fatal("first row/null shape", fields)
			}
			want := 1
			if exact && count > 0 {
				want = 2
			}
			if rows.at != want {
				t.Fatal("unbounded row demand", rows.at, want)
			}
		}
	}
	failure := errors.New("second projection")
	rows := &captureDemandRows{values: [][]sqltype.Value{{one}, {two}, {one}}, failAt: 2, failure: failure}
	if _, present, many, err := core.DemandPLCapture(context.Background(), rows, columns, true); !errors.Is(err, failure) || present || many || rows.at != 2 {
		t.Fatal("second projection error precedence", present, many, err)
	}
	rows.at = 0
	if _, present, many, err := core.DemandPLCapture(context.Background(), rows, columns, false); err != nil || !present || many || rows.at != 1 {
		t.Fatal("nonstrict demanded failing second projection", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rows = &captureDemandRows{values: [][]sqltype.Value{{one}, {two}}, cancel: cancel}
	if _, present, many, err := core.DemandPLCapture(ctx, rows, columns, true); !errors.Is(err, context.Canceled) || present || many || rows.at != 1 {
		t.Fatal("cancellation published row/cardinality", err)
	}
	command := dml.Command{Capture: &dml.CaptureCardinality{}}
	if err := command.CapturedRow(); err != nil {
		t.Fatal(err)
	}
	var excess *dml.CaptureCardinality
	if err := command.CapturedRow(); !errors.As(err, &excess) || excess.Count != 2 {
		t.Fatal("second successful DML projection did not signal", err)
	}
	nested := dml.Command{}
	if err := nested.CapturedRow(); err != nil || command.Capture.Count != 2 {
		t.Fatal("capture leaked into nested command", err)
	}
	t.Run("close_and_activation_unwind", func(t *testing.T) {
		s, _, f, read := loopUnitFrame(t, `BEGIN RETURN 7; END`)
		previous := core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s))
		restore := core.ScopeActivateSQLRoutine(s, nil, read)
		core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(s), f)
		fatal := errors.New("fatal close")
		main := &captureCloseRows{failure: fatal}
		auxiliary := &captureCloseRows{}
		primary := &sqlstate.Error{Code: "P0003", Message: "query returned more than one row"}
		err := core.ClosePLCapture(core.Result{Rows: main, RowSets: []exec.RowResult{auxiliary}}, primary, func(err error) error { return err })
		err = restore(err)
		var diagnostic *sqlstate.Error
		if !errors.Is(err, fatal) || !errors.Is(err, primary) || !errors.As(err, &diagnostic) || diagnostic.Code != "P0003" || !main.closed || !auxiliary.closed || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != previous {
			t.Fatal("lost primary/cleanup/activation", err)
		}
	})
}
