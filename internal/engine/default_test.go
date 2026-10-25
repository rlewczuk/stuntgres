package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A closed, bounded test-only producer witnesses actual stored row values;
// there is no production callback, function registry or new SQL capability.
type defaultOrdinal struct {
	expr.Info
	ordinal int64
	failAt  int64
	cancel  context.CancelFunc
}

func (e *defaultOrdinal) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := exec.EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	e.ordinal++
	if e.ordinal == e.failAt {
		if e.cancel != nil {
			e.cancel()
			return sqltype.Value{}, c.Context.Err()
		}
		return sqltype.Value{}, core.SQLError("22003", "integer out of range")
	}
	return sqltype.Integer(e.Usage.Type, e.ordinal)
}
func defaultProbe(t *testing.T, e *core.Engine, probe *defaultOrdinal) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for address, descriptor := range e.Catalog.State.Descriptors() {
		if old, ok := descriptor.(*catalog.Relation); ok && old.Name() == "d" {
			next := *old
			probe.Info = expr.Info{Usage: old.Columns[1].Usage}
			next.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{old.Attributes[1]: {Expression: probe}}
			// Private descriptor fixture only; published SQL defaults still require the
			// provider's closed-node ownership/serialization validation.
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(address, &next)
			e.Catalog.PublishLocked(state)
			return
		}
	}
	t.Fatal("missing probe relation")
}
func TestDefaultPerRowValuesAndAtomicFailures(t *testing.T) {
	ordinary := [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}
	for _, test := range []struct {
		sql  string
		want [][]driver.Value
	}{
		{`INSERT INTO d(id) VALUES(1),(2),(3)`, ordinary},
		{`INSERT INTO d VALUES(1,DEFAULT),(2,DEFAULT),(3,DEFAULT)`, ordinary},
		{`INSERT INTO d(id) SELECT id FROM src ORDER BY id`, ordinary},
		{`INSERT INTO d(id) VALUES(1),(2),(3) ORDER BY 1`, ordinary},
		{`INSERT INTO d VALUES(1,NULL),(2,9),(3,DEFAULT),(4,DEFAULT),(5,DEFAULT)`, [][]driver.Value{{int64(1), nil}, {int64(2), int64(9)}, {int64(3), int64(1)}, {int64(4), int64(2)}, {int64(5), int64(3)}}},
	} {
		t.Run(test.sql, func(t *testing.T) {
			q := test.sql
			e := New()
			run(t, e, `CREATE TABLE d(id INT,v INT)`)
			run(t, e, `CREATE TABLE src(id INT)`)
			run(t, e, `INSERT INTO src VALUES(1),(2),(3)`)
			probe := &defaultOrdinal{}
			defaultProbe(t, e, probe)
			if n := run(t, e, q).Count; n != int64(len(test.want)) {
				t.Fatal(n)
			}
			want := test.want
			if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			for _, canceling := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				failure := &defaultOrdinal{failAt: 2}
				if canceling {
					failure.cancel = cancel
				}
				defaultProbe(t, e, failure)
				_, err := testExecute(e, ctx, statement(t, q), nil)
				cancel()
				if canceling {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else {
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "22003" || d.Message != "integer out of range" {
						t.Fatal(err)
					}
				}
				if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
					t.Fatal("partial publication", got)
				}
			}
		})
	}
}

func TestDefaultBoundOwnershipTemporalInputAndNoEvaluation(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	a := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement(t, `CREATE TABLE d(i INT DEFAULT 2147483648,t TIMESTAMP DEFAULT 'now',v TIME DEFAULT 'now')`), auth, ids.ObjectRef{}, sqltype.InputEnvironment{TransactionStart: a}, nil, nil)
	var defaults map[int32]catalog.ColumnDefault
	if err == nil {
		defaults, err = bindCreateDefaults(core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{TransactionStart: a}, op, nil), b.Create.Columns, b.Create.Declarations)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("CREATE evaluated deferred narrowing", err)
	}
	for i, d := range defaults {
		if d.Expression.ResultUsage() != b.Create.Columns[i-1].Usage || d.Expression.Properties() != (expr.Properties{}) {
			t.Fatal(d)
		}
	}
	// Rebinding under a different operation clock must not change already-bound leaves.
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, attribute := range []int32{2, 3} {
		value, err := defaults[attribute].Expression.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := value.Type().InputWithEnvironment(context.Background(), "now", -1, sqltype.InputEnvironment{TransactionStart: a})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, expected) {
			t.Fatal(value, expected)
		}
	}
	if _, err := defaults[1].Expression.Eval(c); err == nil {
		t.Fatal("deferred narrowing not applied")
	}
}

func TestDefaultQuerySourceEmptyAndSuppliedBypass(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(id INT,v INT)`)
	run(t, e, `CREATE TABLE src(id INT)`)
	defaultProbe(t, e, &defaultOrdinal{failAt: 1})
	if n := run(t, e, `INSERT INTO d(id) SELECT id FROM src`).Count; n != 0 {
		t.Fatal(n)
	}
	run(t, e, `INSERT INTO src VALUES(1),(2),(3)`)
	if n := run(t, e, `INSERT INTO d(id,v) SELECT id,9 FROM src ORDER BY id`).Count; n != 3 {
		t.Fatal(n)
	}
	want := [][]driver.Value{{int64(1), int64(9)}, {int64(2), int64(9)}, {int64(3), int64(9)}}
	if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestDefaultQuerySourcePreflightKeepsStoredStructure(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(id INT,v INT DEFAULT int4('7'::text))`)
	run(t, e, `CREATE TABLE src(id INT)`)
	ref := roleTableRef(t, e, "d")
	old := e.CatalogRead().MustRelation(ref.Address)
	definition := old.ColumnMeta.Defaults[old.Attributes[1]]
	if n := run(t, e, `INSERT INTO d(id) SELECT id FROM src`).Count; n != 0 {
		t.Fatal(n)
	}
	run(t, e, `INSERT INTO src VALUES(1),(2)`)
	for range 2 {
		if n := run(t, e, `INSERT INTO d(id) SELECT id FROM src ORDER BY id`).Count; n != 2 {
			t.Fatal(n)
		}
		current := e.CatalogRead().MustRelation(ref.Address)
		stored := current.ColumnMeta.Defaults[current.Attributes[1]]
		cast, ok := stored.Expression.(plan.CoercionExpr)
		if !ok || cast.Mode != sqltype.Explicit || !reflect.DeepEqual(stored, definition) {
			t.Fatal("execution preflight replaced stored default", stored, definition)
		}
	}
	want := [][]driver.Value{{int64(1), int64(7)}, {int64(1), int64(7)}, {int64(2), int64(7)}, {int64(2), int64(7)}}
	if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestDefaultQueryPlanningScopedPolicy(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
		{"(NULL IS NULL) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE IS TRUE) OR CAST('oops'::text AS INT)=1", true},
		{"(1 IS NOT DISTINCT FROM 1) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE OR CAST('oops'::text AS INT)=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE d(b BOOL DEFAULT ("+tc.expression+"))")
			view := e.CatalogRead()
			ref := roleTableRef(t, e, "d")
			d := view.MustRelation(ref.Address)
			original := d.ColumnMeta.Defaults[d.Attributes[0]]
			c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
			planned, changed, err := exec.PreflightCoercionsWithPolicy(c, original.Expression, true)
			if err != nil || !changed {
				t.Fatal(planned, changed, err)
			}
			literal, ok := planned.(plan.LiteralExpr)
			if !ok || literal.Info != original.Expression.(plan.LogicalExpr).Info {
				t.Fatal("whole result or conservative metadata was lost", planned)
			}
			value, err := literal.Eval(c)
			if err != nil {
				t.Fatal(err)
			}
			truth, err := plan.TruthOf(value)
			if err != nil || truth == plan.Unknown || (truth == plan.True) != tc.want {
				t.Fatal(truth, err, tc.want)
			}
			// The same original node still demands both branches under ordinary
			// preflight. No flag or execution-folded node leaks into the catalog.
			_, _, err = exec.PreflightCoercions(c, original.Expression)
			state(t, err, "22P02")
			if err := (core.RelationProvider).Validate(view, d); err != nil {
				t.Fatal("execution planning changed the stored definition", err)
			}
			if !reflect.DeepEqual(d.ColumnMeta.Defaults[d.Attributes[0]], original) {
				t.Fatal("stored expression was replaced")
			}
		})
	}
}

func TestDefaultValuesPlanningRuntimeSuppliedBypass(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(id INT,v INT)`)
	probe := &defaultOrdinal{failAt: 1}
	defaultProbe(t, e, probe)
	if n := run(t, e, `INSERT INTO d VALUES(1,NULL),(2,9)`).Count; n != 2 {
		t.Fatal(n)
	}
	if probe.ordinal != 0 {
		t.Fatal("unused runtime producer demanded", probe.ordinal)
	}
	want := [][]driver.Value{{int64(1), nil}, {int64(2), int64(9)}}
	if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestDefaultValuesPlanningSuppliedPriority(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(id INT,v INT DEFAULT int4('declared'::text))`)
	run(t, e, `INSERT INTO d VALUES(0,9)`)
	_, err := testExecute(e, context.Background(), statement(t, `INSERT INTO d(id) VALUES(int4('supplied'::text))`), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type integer: "supplied"` {
		t.Fatal("supplied-value preflight lost priority", err)
	}
	want := [][]driver.Value{{int64(0), int64(9)}}
	if got := tuples(run(t, e, `SELECT * FROM d ORDER BY id`)); !reflect.DeepEqual(got, want) {
		t.Fatal("partial publication", got)
	}
}
