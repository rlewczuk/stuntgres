package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineClosedFormalBoundary(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	integer := enginetest.Usage(e, sqltype.Int4)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for _, count := range []int{0, 1, 2} {
		b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
		b.Params.ClosedRoutineFormals = true
		inputs := make([]sqltype.Usage, count)
		for i := range inputs {
			inputs[i] = integer
		}
		b.SeedParameters(inputs)
		n := count + 1
		_, err := b.Bind(parser.ParameterExpr{Number: n, Pos: parser.Position{Character: 9}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: integer})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P02" || d.Position != "9" || d.Message != fmt.Sprintf("there is no parameter $%d", n) {
			t.Fatalf("count=%d: %v", count, err)
		}
		if len(b.Params.Parameters) != count {
			t.Fatal("out-of-range slot entered inference")
		}
		if err := b.Constrain(n, integer, parser.Position{}); err == nil {
			t.Fatal("constraint escaped closed formals")
		}
		info, err := b.Finish()
		if err != nil || info.Count != count {
			t.Fatal("unused declared slots lost", info, err)
		}
	}
	// Ordinary prepared input inference is deliberately still open.
	b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
	if _, err := b.Bind(parser.ParameterExpr{Number: 1}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: integer}); err != nil {
		t.Fatal(err)
	}
	if info, err := b.Finish(); err != nil || info.Count != 1 {
		t.Fatal(info, err)
	}
}

func TestSQLRoutineReturnContract(t *testing.T) {
	e := engine.New()
	integer, wide, text := enginetest.Usage(e, sqltype.Int4), enginetest.Usage(e, sqltype.Int8), enginetest.Usage(e, sqltype.Text)
	for _, columns := range [][]catalog.Column{nil, {{Usage: integer}, {Usage: integer}}, {{Usage: text}}} {
		_, err := core.ValidateSQLRoutineResult(columns, integer)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P13" || d.Context != "" {
			t.Fatal(err)
		}
	}
	if _, err := core.ValidateSQLRoutineResult([]catalog.Column{{Usage: wide}}, integer); err != nil {
		t.Fatal("assignment conversion rejected", err)
	}
}

func TestSQLRoutineDepthUnwind(t *testing.T) {
	s := &core.StatementScope{}
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		func() {
			var descend func(int) error
			descend = func(n int) error {
				if err := core.ScopeEnterSQLRoutine(s); err != nil {
					return err
				}
				defer core.ScopeLeaveSQLRoutine(s)
				if n == 0 {
					return primary
				}
				return descend(n - 1)
			}
			if err := descend(12); !errors.Is(err, primary) {
				t.Fatal(err)
			}
		}()
		if core.ScopeActiveSQLRoutineDepth(s) != 0 {
			t.Fatal("unbalanced body exit", core.ScopeActiveSQLRoutineDepth(s))
		}
	}
	for range core.MaxSQLRoutineDepth {
		if err := core.ScopeEnterSQLRoutine(s); err != nil {
			t.Fatal(err)
		}
	}
	var d *sqlstate.Error
	if err := core.ScopeEnterSQLRoutine(s); !errors.As(err, &d) || d.Code != "54001" || d.Hint != "" {
		t.Fatal(err)
	}
	if core.ScopeActiveSQLRoutineDepth(s) != core.MaxSQLRoutineDepth {
		t.Fatal("rejected entry changed depth")
	}
	for range core.MaxSQLRoutineDepth {
		core.ScopeLeaveSQLRoutine(s)
	}
	if err := core.ScopeEnterSQLRoutine(s); err != nil {
		t.Fatal("subsequent call rejected", err)
	}
	core.ScopeLeaveSQLRoutine(s)
}

func TestSQLRoutineResultScratchUnwind(t *testing.T) {
	e := engine.New()
	s := core.NewScope(core.ScopeFields{SQLRoutine: core.NewRoutineDemand(core.RoutineDemandFields{Result: enginetest.Usage(e, sqltype.Int8)}), ActiveSQLRoutineDepth: 7})
	outer := core.RoutineDemandResult(core.ScopeSQLRoutineRef(s))
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		restore := core.ScopeActivateSQLRoutine(s, nil, catalog.Read{})
		if core.ScopeActiveSQLRoutineDepth(s) != 7 || !core.RoutineDemandScalar(core.ScopeSQLRoutineRef(s)) || core.RoutineDemandResult(core.ScopeSQLRoutineRef(s)).Type != nil {
			t.Fatal("activation leaked owner or result")
		}
		core.SetRoutineDemandResult(core.ScopeSQLRoutineRef(s), enginetest.Usage(e, sqltype.Int4))
		if err := restore(primary); !errors.Is(err, primary) {
			t.Fatal(err)
		}
		if core.RoutineDemandResult(core.ScopeSQLRoutineRef(s)) != outer || core.ScopeActiveSQLRoutineDepth(s) != 7 || core.RoutineDemandScalar(core.ScopeSQLRoutineRef(s)) {
			t.Fatal("result/depth scratch unwind failed")
		}
	}
}
