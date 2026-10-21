package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func conflictUpdateAnalyzedScope(t *testing.T, e *Engine, s *session.Session, q string) (*session.Operation, *StatementScope, analyze.Binding) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
		op.Done()
	})
	stmt := statement(t, q)
	if err := scope.gate(stmt); err != nil {
		t.Fatal(err)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = describe(op.Context(), e.analysisHost(op), stmt, auth, ref, scope.input, nil, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.input, Output: scope.output, Host: hostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	conflict := dml.NewInsertConflictState(b.Insert.Conflict)
	if err := conflict.SelectArbiters(&evaluation, b.Relation, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if err := preflightConflictUpdate(conflict, &evaluation); err != nil {
		t.Fatal(err)
	}
	scope.InsertConflict = conflict
	return op, scope, b
}

func TestInsertConflictUpdateBinding(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
	sessionSQL(t, s, "CREATE TABLE src(k INT,v INT)")
	op, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t AS dst SELECT k,v FROM src ON CONFLICT(k) DO UPDATE SET v=dst.v+excluded.v WHERE excluded.k>0")
	c := b.Insert.Conflict
	if !reflect.DeepEqual(b.Targets, []int{0, 1}) || !reflect.DeepEqual(c.UpdateTargets, []int{1}) || !reflect.DeepEqual(c.Reads, []int{0}) || !reflect.DeepEqual(c.ActionReads, []int{1, 0}) || len(c.Sources) != 1 || !reflect.DeepEqual(c.Sources[0].Uses, []int{0, 1}) {
		t.Fatal("distinct INSERT/UPDATE/action/source manifests", b.Targets, c.UpdateTargets, c.Reads, c.ActionReads, c.Sources)
	}
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	old := []sqltype.Value{integer(10), integer(1)}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{old[0], old[1], integer(10), integer(2)}}}
	values, err := evaluateUpdateAssignments(&eval, c.Update, c.UpdateTargets, old)
	if err != nil || testExport(values[1]) != int64(3) || testExport(old[1]) != int64(1) {
		t.Fatal("target/excluded slots and immutable RHS", values, old, err)
	}
	unique, err := dml.BindUniqueWrite(scope.writeHost(), b.Ref)
	if err != nil || !reflect.DeepEqual(unique.KeySlots, []int{0}) {
		t.Fatal("installed simple/nonpartial FK-key bitmap", unique, err)
	}
	analyzeConflict := func(q string) (analyze.Binding, error) {
		statements, err := parser.Parse(q)
		if err != nil {
			return analyze.Binding{}, fmt.Errorf("parse %q: %w", q, err)
		}
		if len(statements) != 1 {
			return analyze.Binding{}, fmt.Errorf("parse %q: expected one statement, got %d", q, len(statements))
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return analyze.Binding{}, err
		}
		bound, err := describe(op.Context(), e.analysisHost(op), statements[0], auth, b.Ref, scope.input, nil, nil)
		if err == nil {
			scope.routineAuth, scope.routineDependencies = auth, bound.Dependencies
		}
		return bound, err
	}
	parameters, err := analyzeConflict("INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=$1 WHERE $2")
	if err != nil || parameters.Parameters.Count != 2 || parameters.Parameters.Types[0].Type.ID() != sqltype.Int4 || parameters.Parameters.Types[1].Type.ID() != sqltype.Bool {
		t.Fatal("action-only contextual parameter manifest", parameters.Parameters, err)
	}
	for _, tc := range []struct{ q, code, message, position string }{
		{"INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=$1 WHERE $1", "42804", "argument of WHERE must be type boolean, not type integer", "67"},
		{"INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=nope WHERE 1", "42703", `column "nope" does not exist`, "58"},
	} {
		_, err := analyzeConflict(tc.q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Position != tc.position {
			t.Fatal("SET precedes WHERE, with shared parameter captures", tc, err)
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Parameters: []sqltype.Value{integer(1)}, Input: scope.input, Output: scope.output, Host: hostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, tc := range []struct {
		name, query string
		error       bool
		unchanged   bool
	}{
		{"constant_false", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=1/0 WHERE FALSE", true, false},
		{"constant_null", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=1/0 WHERE NULL", true, false},
		{"constant_no_hit", "INSERT INTO t VALUES(2,0) ON CONFLICT(k) DO UPDATE SET v=1/0", true, false},
		{"row_dependent", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=t.v/0 WHERE FALSE", false, true},
		{"parameter", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=CAST($1 AS INTEGER)/0 WHERE FALSE", false, true},
		{"volatile", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=random() WHERE FALSE", false, true},
		{"subquery", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=(SELECT 1/0) WHERE FALSE", false, true},
		{"constant_value", "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=1+2 WHERE FALSE", false, false},
	} {
		action, err := analyzeConflict(tc.query)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		original := action.Insert.Conflict.Update
		err = preflightConflictUpdate(dml.NewInsertConflictState(action.Insert.Conflict), &evaluation)
		if tc.error {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22012" || d.Message != "division by zero" || action.Insert.Conflict.Update != original {
				t.Fatal("immutable action planning error without plan mutation", tc.name, err)
			}
			continue
		}
		if err != nil || (tc.unchanged && action.Insert.Conflict.Update != original) {
			t.Fatal("runtime-owned SET remains unevaluated and unchanged", tc.name, err)
		}
		if !tc.unchanged {
			if action.Insert.Conflict.Update == original {
				t.Fatal("constant action requires private execution tree")
			}
			value, err := action.Insert.Conflict.Update.Assignments[0].Eval(&evaluation)
			if err != nil || testExport(value) != int64(3) {
				t.Fatal(value, err)
			}
			if _, literal := original.Assignments[0].(plan.LiteralExpr); literal {
				t.Fatal("analyzed action was mutated")
			}
		}
	}
	t.Run("array_bound_constant", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,a INT[])")
		sessionSQL(t, s, "INSERT INTO t VALUES(1,'{1}')")
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.Execute(op, statement(t, "INSERT INTO t VALUES(2,'{2}'),(1,'{3}') ON CONFLICT(k) DO UPDATE SET a[1/0]=excluded.a[1] WHERE FALSE"), 0, nil)
		op.Done()
		conflictUpdateWantError(t, conflictUpdateWorkerResult{result, err}, "22012", "division by zero")
		transactionRows(t, s, "SELECT k FROM t", 1)
		lockEmpty(t, e)
	})
	t.Run("hierarchy_boundary", func(t *testing.T) {
		e := New()
		admin := sessionStart(t, e, nil)
		worker := sessionStart(t, e, nil)
		sessionSQL(t, admin, "CREATE SEQUENCE hierarchy_seq")
		sessionSQL(t, admin, "CREATE TABLE parent(k INT,v INT DEFAULT nextval('hierarchy_seq')) PARTITION BY RANGE(k)")
		sessionSQL(t, admin, "CREATE TABLE leaf PARTITION OF parent FOR VALUES FROM(0) TO(10)")
		sessionSQL(t, admin, "CREATE ROLE hierarchy_actor")
		for _, table := range []string{"parent", "leaf"} {
			sessionSQL(t, admin, "GRANT INSERT, SELECT ON "+table+" TO hierarchy_actor")
		}
		sessionSQL(t, worker, "SET ROLE hierarchy_actor")
		check := func(query, code, message string) {
			t.Helper()
			op, err := worker.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, query), 0, nil)
			op.Done()
			conflictUpdateWantError(t, conflictUpdateWorkerResult{result, err}, code, message)
			lockEmpty(t, e)
		}
		for _, table := range []string{"parent", "leaf"} {
			prefix := "INSERT INTO " + table + "(k) VALUES(1) ON CONFLICT(k) DO UPDATE SET "
			check(prefix+"v=absent", "42703", `column "absent" does not exist`)
			check(prefix+"v=excluded.v WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer")
			check(prefix+"v=excluded.v", "42501", "permission denied for table "+table)
			sessionSQL(t, admin, "GRANT UPDATE ON "+table+" TO hierarchy_actor")
			check(prefix+"v=excluded.v", "0A000", "ON CONFLICT on partition hierarchies is unsupported")
		}
		transactionRows(t, admin, "SELECT nextval('hierarchy_seq')", 1)
		transactionRows(t, admin, "SELECT count(*) FROM parent", 0)
		lockEmpty(t, e)
	})
}

func TestInsertConflictUpdateDuplicateIdentity(t *testing.T) {
	for _, tc := range []struct {
		first, second, count int64
		code                 string
	}{{0, 0, 0, ""}, {0, 1, 1, ""}, {1, 0, 0, "21000"}, {1, 1, 0, "21000"}} {
		t.Run(fmt.Sprintf("%d_%d", tc.first, tc.second), func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			old := e.storage.Relations[ref.Address].Versions[0]
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, fmt.Sprintf("INSERT INTO t VALUES(1,%d),(1,%d) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE excluded.v>0", tc.first, tc.second)), 0, nil)
			op.Done()
			if tc.code != "" {
				conflictUpdateWantError(t, conflictUpdateWorkerResult{result, err}, tc.code, "ON CONFLICT DO UPDATE command cannot affect row a second time")
			} else if err != nil || result.Count != tc.count || result.Rows != nil {
				t.Fatal(tc, result, err)
			}
			want := int64(10)
			if tc.count == 1 {
				want = 1
			}
			transactionRows(t, s, "SELECT v FROM t", want)
			head := e.storage.Relations[ref.Address].Versions[0]
			if head.Row != old.Row || (tc.count == 0 && head.Version != old.Version) || testExport(old.Values[1]) != int64(10) {
				t.Fatal("false filtering or failure replaced original identity", head, old)
			}
			lockEmpty(t, e)
		})
	}
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	original := e.storage.Relations[ref.Address].Versions[0]
	r := sessionSQL(t, s, "INSERT INTO t VALUES(1,0),(1,11) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE excluded.v>0")
	if r.Count != 1 || e.storage.Relations[ref.Address].Versions[0].Row != original.Row {
		t.Fatal("false then true identity", r)
	}
	for _, q := range []string{
		"INSERT INTO t VALUES(2,20),(2,21) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE",
		"INSERT INTO t VALUES(1,1),(11,2) ON CONFLICT(k) DO UPDATE SET k=excluded.k+10,v=excluded.v",
	} {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.Execute(op, statement(t, q), 0, nil)
		op.Done()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "21000" || d.Message != "ON CONFLICT DO UPDATE command cannot affect row a second time" || d.Hint != "Ensure that no rows proposed for insertion within the same command have duplicate constrained values." || result.Rows != nil || result.Count != 0 {
			t.Fatal("actual affected RowID cardinality", q, result, err)
		}
		transactionRows(t, s, "SELECT v FROM t", 11)
		if e.storage.Relations[ref.Address].Versions[0].Row != original.Row {
			t.Fatal("identity changed on failure")
		}
	}
	moved := sessionSQL(t, s, "INSERT INTO t VALUES(1,1),(1,2) ON CONFLICT(k) DO UPDATE SET k=excluded.k+10,v=excluded.v")
	if moved.Count != 2 {
		t.Fatal("same source key can admit a genuinely different RowID after key move", moved)
	}
	transactionRows(t, s, "SELECT v FROM t ORDER BY k", 2, 1)
	if e.storage.Relations[ref.Address].Versions[0].Row != original.Row {
		t.Fatal("moved target lost stable identity")
	}
	lockEmpty(t, e)
}

func TestInsertConflictUpdateCursorFailure(t *testing.T) {
	for _, failure := range []string{"next", "close", "primary_and_close"} {
		e := New()
		s := sessionStart(t, e, nil)
		observer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
		_, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t SELECT k,v FROM t ON CONFLICT(k) DO UPDATE SET v=excluded.v")
		integer := func(n int64) sqltype.Value {
			v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		injected := errors.New("producer veto")
		secondary := errors.New("close veto")
		cursor := &insertConflictInputCursor{schema: b.Relation.Columns, rows: [][]sqltype.Value{{integer(2), integer(20)}, {integer(1), integer(11)}}}
		if failure != "close" {
			cursor.nextError = injected
		}
		if failure == "close" {
			cursor.closeError = injected
		}
		if failure == "primary_and_close" {
			cursor.closeError = secondary
		}
		rows, err := assembleInsertRows(scope.op.Context(), b, cursor, -1, "public", scope.input, scope.output, scope, true, nil, nil)
		if !errors.Is(err, injected) || rows.Values != nil || cursor.closes != 1 || len(scope.stage.rows[b.Ref.Address]) == 0 {
			t.Fatal("cursor failure after actual mixed-stage prefix", rows, err, cursor.closes, scope.stage.rows)
		}
		consumed, affected := 1, 1
		if failure == "close" {
			consumed, affected = 2, 2
		}
		if cursor.position != consumed || cursor.nextCalls != consumed+1 || scope.InsertConflict.Affected != affected {
			t.Fatal("consumed proposals and once-only mixed action accounting", cursor.position, cursor.nextCalls, scope.InsertConflict.Affected)
		}
		if finishErr := scope.Finish(err); !errors.Is(finishErr, injected) {
			t.Fatal("primary veto lost", finishErr)
		}
		scope.op.Done()
		transactionRows(t, observer, "SELECT v FROM t", 10)
		lockEmpty(t, e)
	}
}
