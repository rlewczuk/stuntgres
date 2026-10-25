package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineClosedFormalBoundary(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	integer := testUsage(e, sqltype.Int4)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for _, count := range []int{0, 1, 2} {
		b := NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
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
	b := NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
	if _, err := b.Bind(parser.ParameterExpr{Number: 1}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: integer}); err != nil {
		t.Fatal(err)
	}
	if info, err := b.Finish(); err != nil || info.Count != 1 {
		t.Fatal(info, err)
	}
}

func TestSQLRoutineReturnContract(t *testing.T) {
	e := New()
	integer, wide, text := testUsage(e, sqltype.Int4), testUsage(e, sqltype.Int8), testUsage(e, sqltype.Text)
	for _, columns := range [][]catalog.Column{nil, {{Usage: integer}, {Usage: integer}}, {{Usage: text}}} {
		_, err := ValidateSQLRoutineResult(columns, integer)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P13" || d.Context != "" {
			t.Fatal(err)
		}
	}
	if _, err := ValidateSQLRoutineResult([]catalog.Column{{Usage: wide}}, integer); err != nil {
		t.Fatal("assignment conversion rejected", err)
	}
}

func TestSQLRoutineDepthUnwind(t *testing.T) {
	s := &StatementScope{}
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		func() {
			var descend func(int) error
			descend = func(n int) error {
				if err := ScopeEnterSQLRoutine(s); err != nil {
					return err
				}
				defer ScopeLeaveSQLRoutine(s)
				if n == 0 {
					return primary
				}
				return descend(n - 1)
			}
			if err := descend(12); !errors.Is(err, primary) {
				t.Fatal(err)
			}
		}()
		if ScopeActiveSQLRoutineDepth(s) != 0 {
			t.Fatal("unbalanced body exit", ScopeActiveSQLRoutineDepth(s))
		}
	}
	for range MaxSQLRoutineDepth {
		if err := ScopeEnterSQLRoutine(s); err != nil {
			t.Fatal(err)
		}
	}
	var d *sqlstate.Error
	if err := ScopeEnterSQLRoutine(s); !errors.As(err, &d) || d.Code != "54001" || d.Hint != "" {
		t.Fatal(err)
	}
	if ScopeActiveSQLRoutineDepth(s) != MaxSQLRoutineDepth {
		t.Fatal("rejected entry changed depth")
	}
	for range MaxSQLRoutineDepth {
		ScopeLeaveSQLRoutine(s)
	}
	if err := ScopeEnterSQLRoutine(s); err != nil {
		t.Fatal("subsequent call rejected", err)
	}
	ScopeLeaveSQLRoutine(s)
}

func TestSQLRoutineResultScratchUnwind(t *testing.T) {
	e := New()
	s := NewScope(ScopeFields{SQLRoutine: NewRoutineDemand(RoutineDemandFields{Result: testUsage(e, sqltype.Int8)}), ActiveSQLRoutineDepth: 7})
	outer := RoutineDemandResult(ScopeSQLRoutineRef(s))
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		restore := ScopeActivateSQLRoutine(s, nil, catalog.Read{})
		if ScopeActiveSQLRoutineDepth(s) != 7 || !RoutineDemandScalar(ScopeSQLRoutineRef(s)) || RoutineDemandResult(ScopeSQLRoutineRef(s)).Type != nil {
			t.Fatal("activation leaked owner or result")
		}
		SetRoutineDemandResult(ScopeSQLRoutineRef(s), testUsage(e, sqltype.Int4))
		if err := restore(primary); !errors.Is(err, primary) {
			t.Fatal(err)
		}
		if RoutineDemandResult(ScopeSQLRoutineRef(s)) != outer || ScopeActiveSQLRoutineDepth(s) != 7 || RoutineDemandScalar(ScopeSQLRoutineRef(s)) {
			t.Fatal("result/depth scratch unwind failed")
		}
	}
}
