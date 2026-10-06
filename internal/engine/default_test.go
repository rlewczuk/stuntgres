package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A closed, bounded test-only producer witnesses actual stored row values;
// there is no production callback, function registry or new SQL capability.
type defaultOrdinal struct {
	expressionInfo
	ordinal int64
	failAt  int64
	cancel  context.CancelFunc
}

func (e *defaultOrdinal) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	e.ordinal++
	if e.ordinal == e.failAt {
		if e.cancel != nil {
			e.cancel()
			return sqltype.Value{}, c.Context.Err()
		}
		return sqltype.Value{}, sqlError("22003", "integer out of range")
	}
	return sqltype.Integer(e.usage.Type, e.ordinal)
}
func defaultProbe(t *testing.T, e *Engine, probe *defaultOrdinal) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for address, descriptor := range e.state.descriptors {
		if old, ok := descriptor.(*relationDescriptor); ok && old.name == "d" {
			next := *old
			probe.expressionInfo = expressionInfo{usage: old.columns[1].Usage}
			next.defaults = map[int32]columnDefault{old.attributes[1]: {expression: probe, sql: "0"}}
			// Private descriptor fixture only; published SQL defaults still require the
			// provider's closed-node ownership/serialization validation.
			state := cloneCatalog(e.state)
			state.descriptors[address] = &next
			e.state = state
			return
		}
	}
	t.Fatal("missing probe relation")
}
func TestDefaultPerRowValuesAndAtomicFailures(t *testing.T) {
	for _, q := range []string{`INSERT INTO d(id) VALUES(1),(2),(3)`, `INSERT INTO d VALUES(1,DEFAULT),(2,DEFAULT),(3,DEFAULT)`} {
		t.Run(q, func(t *testing.T) {
			e := New()
			run(t, e, `CREATE TABLE d(id INT,v INT)`)
			probe := &defaultOrdinal{}
			defaultProbe(t, e, probe)
			if n := run(t, e, q).Count; n != 3 {
				t.Fatal(n)
			}
			want := [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}
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
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, statement(t, `CREATE TABLE d(i INT DEFAULT 2147483648,t TIMESTAMP DEFAULT 'now',v TIME DEFAULT 'now')`), auth, ObjectRef{}, sqltype.InputEnvironment{TransactionStart: a}, op, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal("CREATE evaluated deferred narrowing", err)
	}
	for i, d := range b.createDefaults {
		if d.expression.ResultUsage() != b.createColumns[i-1].Usage || d.expression.Properties() != (ExprProperties{}) {
			t.Fatal(d)
		}
	}
	// Rebinding under a different operation clock must not change already-bound leaves.
	c := &EvalContext{Context: context.Background()}
	for _, attribute := range []int32{2, 3} {
		value, err := b.createDefaults[attribute].expression.Eval(c)
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
	if _, err := b.createDefaults[1].expression.Eval(c); err == nil {
		t.Fatal("deferred narrowing not applied")
	}
}
