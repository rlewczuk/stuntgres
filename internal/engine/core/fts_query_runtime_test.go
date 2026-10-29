package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFTSQueryFunctionsDiagnosticPhases(t *testing.T) {
	prepare := &sqlstate.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "15"}
	before := *prepare
	got := exec.RewriteSQLDiagnostic(prepare, "/*é猫*/ SELECT missing").(*sqlstate.Error)
	if got == prepare || *prepare != before || got.Position != "" || got.InternalPosition != "15" || got.InternalQuery != "/*é猫*/ SELECT missing" || got.Context != "" {
		t.Fatal("prepare location ownership", got)
	}
	open := &sqlstate.Error{Code: "22012", Message: "division by zero"}
	got = exec.RewriteSQLDiagnostic(open, "SELECT 1/0").(*sqlstate.Error)
	if got.Context != `SQL statement "SELECT 1/0"` || open.Context != "" {
		t.Fatal("open error context", got)
	}
	inner := &sqlstate.Error{Code: "42601", Message: "inner", InternalPosition: "8", InternalQuery: "nested", Context: "prior"}
	if exec.RewriteSQLDiagnostic(inner, "outer") != inner {
		t.Fatal("nested diagnostic overwritten")
	}
	if exec.RewriteSQLDiagnostic(context.Canceled, "outer") != context.Canceled {
		t.Fatal("ordinary cancellation wrapped")
	}
	for _, tc := range []struct {
		statement parser.Statement
		tuple     bool
		violation string
	}{
		{&parser.Select{}, true, ""},
		{&parser.Select{Locking: []parser.RowLockClause{{Mode: "FOR UPDATE"}}}, true, "SELECT FOR UPDATE"},
		{&parser.Insert{}, false, "INSERT"},
		{&parser.Insert{Returning: &parser.Returning{}}, true, "INSERT"},
		{&parser.Select{With: []parser.CommonTableExpression{{Query: &parser.Insert{}}}}, true, "SELECT"},
	} {
		if core.NestedTupleStatement(tc.statement) != tc.tuple || core.NestedReadOnlyViolation(tc.statement) != tc.violation {
			t.Fatal("cursor/read-only admission", tc)
		}
	}
}

func TestFTSQueryFunctionsProducerDemand(t *testing.T) {
	ctx := context.Background()
	var integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	columns := []catalog.Column{{Name: "v", Usage: integer.Usage()}}
	value, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	root := &plan.ProjectNode{QuerySchema: columns, Child: &plan.SourceNode{QuerySchema: columns}, Frame: 1, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}}}
	source := &exec.Source{Anonymous: [][]sqltype.Value{{value}, {value}, {value}}}
	rows, err := exec.OpenNestedQueryRows(ctx, root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows.Schema(), columns) {
		t.Fatal("schema unavailable before Next")
	}
	var row [1]sqltype.Value
	for range 3 {
		present, err := rows.Next(ctx, row[:])
		if err != nil || !present || row[0] != value {
			t.Fatal("ordered all-row demand", present, row, err)
		}
	}
	if present, err := rows.Next(ctx, row[:]); present || err != nil {
		t.Fatal("exhaustion", present, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal("idempotent close", err)
	}
	if _, err := rows.Next(ctx, row[:]); err == nil {
		t.Fatal("read after close")
	}
	// An ordinary reached expression error is a fetch error, never retroactively
	// decorated as an SPI open error. No SQL or alternate rule executor is used.
	failure := errors.New("reached projection failure")
	calls := 0
	root.Expressions[0] = queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	rows, err = exec.OpenNestedQueryRows(ctx, root, &exec.Source{Anonymous: [][]sqltype.Value{{value}}}, nil)
	if err != nil || calls != 0 {
		t.Fatal("open demanded projection", calls, err)
	}
	if _, err := rows.Next(ctx, row[:]); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("fetch provenance", calls, err)
	}
	_ = rows.Close()
	root.Expressions[0] = plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	rows, err = exec.OpenNestedQueryRows(ctx, root, &exec.Source{Anonymous: [][]sqltype.Value{{value}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := rows.Next(canceled, row[:]); !errors.Is(err, context.Canceled) {
		t.Fatal("fetch cancellation", err)
	}
	_ = rows.Close()
}

func TestFTSQueryFunctionsScratchRestoration(t *testing.T) {
	address := ids.ObjectAddress{ObjectID: 90001}
	original := []storage.RowVersion{{Row: 1, Version: 1}}
	s := core.NewScope(core.ScopeFields{Expected: &core.Description{}, SQLRoutine: core.NewRoutineDemand(core.RoutineDemandFields{Scalar: true})})
	s.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{address: original}
	s.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{address: {{Row: 2, Version: 2}}}
	dependencies := []catalog.ExpressionDependency{{Revision: 7}}
	s.RoutineDependencies = dependencies
	expected := s.Expected
	prior := s.OwnWrites[address]
	restore := s.ActivateSQLRoutine(nil, catalog.Read{})
	if s.RoutineDependencies != nil {
		t.Fatal("inner scope inherited outer dependencies")
	}
	innerDependencies := []catalog.ExpressionDependency{{Revision: 9}}
	s.RoutineDependencies = innerDependencies
	core.SetRoutineDemandScalar(core.ScopeSQLRoutineRef(s), false)
	core.SetRoutineDemandNestedCursor(core.ScopeSQLRoutineRef(s), true)
	if len(s.OwnWrites[address]) != 2 || len(prior) != 1 {
		t.Fatal("private staged visibility")
	}
	nested := s.ActivateSQLRoutine(nil, catalog.Read{})
	core.SetRoutineDemandNestedCursor(core.ScopeSQLRoutineRef(s), false)
	if err := nested(nil); err != nil || !core.RoutineDemandNestedCursor(core.ScopeSQLRoutineRef(s)) || len(s.RoutineDependencies) != 1 || &s.RoutineDependencies[0] != &innerDependencies[0] {
		t.Fatal("stackwise restore", err)
	}
	failure := errors.New("primary")
	if restore(failure) != failure || s.Expected != expected || !core.RoutineDemandScalar(core.ScopeSQLRoutineRef(s)) || core.RoutineDemandNestedCursor(core.ScopeSQLRoutineRef(s)) || len(s.OwnWrites[address]) != 1 || len(s.RoutineDependencies) != 1 || &s.RoutineDependencies[0] != &dependencies[0] {
		t.Fatal("error scratch restoration")
	}
}
