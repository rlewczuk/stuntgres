package binder_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func TestSmallintStagedCancellation(t *testing.T) {
	// INSERT validates each physical row before StageInsert adopts the complete
	// batch. Count only that real validator's demand, not analysis, normalization
	// or INT4-to-INT2 planning. A valid row polls at physical validation entry
	// and at EvaluateGenerations's no-generation boundary.
	for _, tc := range []struct {
		name      string
		check     int32
		validated int
	}{
		{"constraint_pass_start", 1, 0},
		{"during_constraint_pass", 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, cancel, release := smallintCancellationScope(t)
			stmt := enginetest.Statement(t, scope.Op.OriginalSQL)
			if _, err := scope.Describe(stmt); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(scope.Op)
			var binding analyze.Binding
			if err == nil {
				binding, err = core.Describe(scope.Op.Context(), e.AnalysisHost(scope.Op), stmt, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			scope.RoutineAuth = auth
			scope.RoutineDependencies = binding.Dependencies
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{
				Context: scope.ActivateRuntime(scope.Op.Context()), Host: core.HostOf(scope),
				Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output,
			}}
			rows := make([][]sqltype.Value, len(binding.Insert.Rows))
			for i, source := range binding.Insert.Rows {
				rows[i] = make([]sqltype.Value, len(binding.Relation.Columns))
				for j, column := range binding.Relation.Columns {
					rows[i][j] = sqltype.Null(column.Usage.Type)
				}
				if err := dml.EvaluateInsertValuesRow(auth.Catalog, &binding, binding.Expressions[i], source, rows[i], true, &evaluation); err != nil {
					t.Fatal(err)
				}
			}
			smallintCancellationRows(t, rows)
			ctx := &cancelAtErrCheck{Context: evaluation.Context, cancel: cancel, at: tc.check}
			evaluation.Context = ctx
			validated := 0
			for _, row := range rows {
				var proceed bool
				proceed, err = dml.ValidateInsertRow(scope.WriteHost(), binding, "public", row, &evaluation)
				if err != nil {
					break
				}
				if !proceed {
					t.Fatal("unexpected row suppression")
				}
				validated++
			}
			if !errors.Is(err, context.Canceled) || ctx.checks.Load() != tc.check || validated != tc.validated || len(scope.Stage.Rows) != 0 {
				t.Fatal("row constraint boundary", err, ctx.checks.Load(), validated, scope.Stage.Rows)
			}
			var sqlErr interface{ SQLState() string }
			if errors.As(err, &sqlErr) {
				t.Fatal("row cancellation fabricated SQLSTATE", err)
			}
			if finished := scope.Finish(err); !errors.Is(finished, context.Canceled) || ctx.checks.Load() != tc.check {
				t.Fatal("constraint error ownership", finished, ctx.checks.Load())
			}
			if !scope.Finished || scope.Stage.Rows != nil || scope.View != nil || scope.LockTicket != nil {
				t.Fatal("constraint failure retained statement authority", scope.Finished, scope.Stage.Rows, scope.View, scope.LockTicket)
			}
			release()
			smallintCancellationRecovery(t, e)
		})
	}
	for _, tc := range []struct {
		name  string
		check int32
	}{
		{"after_complete_stage", 1},
		{"before_publication", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, cancel, release := smallintCancellationScope(t)
			result, err := scope.Execute(enginetest.Statement(t, scope.Op.OriginalSQL), 0, nil)
			if err != nil || result.Count != 3 || result.Rows != nil || scope.Completed || scope.Finished || len(scope.Stage.Rows) != 1 {
				t.Fatal("incomplete private INSERT", result, err, scope.Completed, scope.Finished, scope.Stage.Rows)
			}
			staged := scope.Stage.Rows[scope.LockedRef.Address]
			rows := make([][]sqltype.Value, len(staged))
			for i, row := range staged {
				if row.Row == 0 || row.Version == 0 || row.Born != 0 {
					t.Fatal("not an unpublished owned row", row)
				}
				rows[i] = row.Values
			}
			smallintCancellationRows(t, rows)
			// With no catalog, index, trigger, FK or HOLD work, Finish polls
			// ValidateOperation first, then PublicationOperationLocked at the
			// final row-publication fence. Normalization has already completed
			// with the collector enabled and the genuine original SQL.
			ctx := &cancelAtErrCheck{Context: scope.Op.Context(), cancel: cancel, at: tc.check}
			scope.Op.Ctx = ctx
			err = scope.Finish(nil)
			if !errors.Is(err, context.Canceled) || ctx.checks.Load() != tc.check || !scope.Finished || scope.Stage.Rows != nil || scope.View != nil || scope.LockTicket != nil {
				t.Fatal("staged publication rejection", err, ctx.checks.Load(), scope.Finished, scope.Stage.Rows, scope.View, scope.LockTicket)
			}
			var sqlErr interface{ SQLState() string }
			if errors.As(err, &sqlErr) {
				t.Fatal("publication cancellation fabricated SQLSTATE", err)
			}
			release()
			smallintCancellationRecovery(t, e)
		})
	}
}

func smallintCancellationScope(t *testing.T) (*core.Engine, *core.StatementScope, context.CancelFunc, func()) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id SMALLINT NOT NULL,label TEXT,flag BOOL)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(99,'sentinel',TRUE)")
	base, cancel := context.WithCancel(context.Background())
	op, cleanup, err := enginetest.Operation(e, base)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		cleanup()
		cancel()
		t.Fatal(err)
	}
	op.OriginalSQL = "INSERT INTO t VALUES(1,'one'),(2,'two'),(3,'three')"
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		cleanup()
		cancel()
	}
	t.Cleanup(release)
	return e, scope, cancel, release
}

func smallintCancellationRows(t *testing.T, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatal("incomplete typed rows", rows)
	}
	for i, row := range rows {
		if len(row) != 3 || row[0].Type().ID() != sqltype.Int2 || row[1].Type().ID() != sqltype.Text || row[2].Type().ID() != sqltype.Bool {
			t.Fatal("lost bound SMALLINT row layout", row)
		}
		id, err := row[0].Integer()
		label, labelErr := row[1].Text()
		if err != nil || id != int64(i+1) || labelErr != nil || label != []string{"one", "two", "three"}[i] || !row[2].IsNull() {
			t.Fatal("changed bound row", row, err, labelErr)
		}
	}
}

func smallintCancellationRecovery(t *testing.T, e *core.Engine) {
	t.Helper()
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(99), "sentinel", true}}) {
		t.Fatal(got)
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES(4)")
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(4), nil, nil}, {int64(99), "sentinel", true}}) {
		t.Fatal(got)
	}
}

func TestSmallintLiteralInference(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(s SMALLINT)")
	result := enginetest.Run(t, e, "SELECT 7,32768,2147483648 FROM t")
	defer result.Rows.Close()
	for i, id := range []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8} {
		if result.Rows.Schema()[i].Usage.Type.ID() != id {
			t.Fatal(result.Rows.Schema()[i])
		}
	}
}
