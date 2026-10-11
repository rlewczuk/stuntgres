package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestSQLCursorHoldOwner(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	c := s.cursors.entries["h"]
	pin, cell, epoch := c.source.view, c.authority, c.epoch
	first := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "COMMIT")
	if !c.promoted || c.tx != (TxRef{}) || c.epoch != epoch || c.root != nil || c.source != nil || c.stage != nil || c.authority != nil || c.leaves != nil || c.locks != nil || c.currentSource != nil || c.state.source != nil || c.state.parameters != nil || pin.data.Load() != nil || cell.scope != nil || cell.expression != nil || c.count != 2 || c.next != 0 {
		t.Fatal("export retains producer or loses owned remainder", c, cell)
	}
	if c.catalog.state != nil || !reflect.DeepEqual(first.Rows.Schema(), c.schema) {
		t.Fatal("export retains declaration catalog or loses owned schema")
	}
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE fresh CURSOR WITH HOLD FOR SELECT 9")
	sessionSQL(t, s, "ROLLBACK")
	if s.cursors.entries["h"] != c || s.cursors.entries["fresh"] != nil {
		t.Fatal("later rollback crosses exported ownership")
	}
	second := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(2)}})
	sessionSQL(t, s, "CLOSE h")
	c.dispose()
	if c.values != nil || c.catalog.state != nil || cell.scope != nil || pin.data.Load() != nil {
		t.Fatal("repeated close retains resources")
	}
	if !reflect.DeepEqual(tuples(first), [][]driver.Value{{int64(1)}}) || !reflect.DeepEqual(tuples(second), [][]driver.Value{{int64(2)}}) {
		t.Fatal("close mutates detached batches")
	}
	sessionSQL(t, s, "DECLARE fenced CURSOR WITH HOLD FOR SELECT 7")
	if err := s.Engine().Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH ALL FROM fenced")
	if !errors.Is(err, ErrCatalogReplaced) || result.Rows != nil {
		t.Fatal("export loses original epoch fence", result, err)
	}
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil || len(s.cursors.entries) != 0 {
		t.Fatal("physical end retains store", err)
	}
	if !reflect.DeepEqual(tuples(second), [][]driver.Value{{int64(2)}}) {
		t.Fatal("physical end mutates detached batch")
	}
}

func TestSQLCursorHoldDemand(t *testing.T) {
	for _, fetched := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(fetched), func(t *testing.T) {
			s := cursorUnitSetup(t)
			sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
			c := s.cursors.entries["h"]
			original := c.root.expressions[0]
			calls := 0
			c.root.expressions[0] = queryProbe(original.ResultUsage(), func(ctx *EvalContext) (sqltype.Value, error) {
				calls++
				return original.Eval(ctx)
			})
			if fetched == 1 {
				cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(1)}})
			} else if fetched >= 3 {
				cursorUnitBatch(t, s, "FETCH 3 FROM h", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
				if fetched == 4 {
					cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{})
				}
			}
			if calls != min(fetched, 3) {
				t.Fatal("FETCH replay/lookahead", calls, fetched)
			}
			sessionSQL(t, s, "COMMIT")
			if calls != 3 || !c.promoted || c.count != 3-min(fetched, 3) {
				t.Fatal("commit reopened/skipped producer", calls, c.count)
			}
			want := [][]driver.Value{}
			for i := min(fetched, 3) + 1; i <= 3; i++ {
				want = append(want, []driver.Value{int64(i)})
			}
			cursorUnitBatch(t, s, "FETCH ALL FROM h", want)
			cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{})
			if calls != 3 {
				t.Fatal("stored FETCH reevaluates targets", calls)
			}
		})
	}
	t.Run("zero_width_and_empty_schema", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE z CURSOR WITH HOLD FOR SELECT FROM unit_rows")
		sessionSQL(t, s, "DECLARE e CURSOR WITH HOLD FOR SELECT i FROM unit_rows WHERE FALSE")
		sessionSQL(t, s, "COMMIT")
		z := s.cursors.entries["z"]
		if z.count != 3 || len(z.schema) != 0 || len(z.values) != 0 {
			t.Fatal("zero-width cardinality lost", z)
		}
		cursorUnitBatch(t, s, "FETCH 1 FROM z", [][]driver.Value{{}})
		cursorUnitBatch(t, s, "FETCH ALL FROM z", [][]driver.Value{{}, {}})
		cursorUnitBatch(t, s, "FETCH ALL FROM z", [][]driver.Value{})
		empty := cursorUnitBatch(t, s, "FETCH ALL FROM e", [][]driver.Value{})
		if len(empty.Rows.Schema()) != 1 || empty.Rows.Schema()[0].Name != "i" {
			t.Fatal("empty export loses schema")
		}
	})
}

func TestSQLCursorHoldAuthority(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, before := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/prior=%t", native, before), func(t *testing.T) {
				s := cursorUnitSetup(t)
				sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
				c, ref := s.cursors.entries["h"], s.transactions.ref
				cell := c.authority
				original := c.root.expressions[0]
				phase, calls := "fetch", 0
				var first *cursorExpressionData
				c.root.expressions[0] = queryProbe(original.ResultUsage(), func(ctx *EvalContext) (sqltype.Value, error) {
					scope := ctx.liveScope()
					if scope == nil || scope.finished || scope.txRef != ref || scope.ticket != s.transactions.ticket || ctx.Context.Value(cursorUnitContextKey{}) != phase || scope.op.done {
						return sqltype.Value{}, errors.New("producer borrows stale commit authority")
					}
					if !scope.owner.mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds transaction mutex")
					}
					scope.owner.mu.Unlock()
					if !s.Engine().mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds engine mutex")
					}
					s.Engine().mu.Unlock()
					if !s.mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds session mutex")
					}
					s.mu.Unlock()
					if first == nil {
						first = cell.expression
					} else if cell.expression != first {
						return sqltype.Value{}, errors.New("commit replaces first-demand expression data")
					}
					if first == nil || first.statementStart.IsZero() {
						return sqltype.Value{}, errors.New("missing first-demand expression data")
					}
					calls++
					return original.Eval(ctx)
				})
				if before {
					result, err := cursorUnitExecute(t, s, context.WithValue(context.Background(), cursorUnitContextKey{}, phase), "FETCH 1 FROM h")
					if err != nil || result.Count != 1 {
						t.Fatal(result, err)
					}
					result.Rows.Close()
				}
				phase = "commit"
				ctx := context.WithValue(context.Background(), cursorUnitContextKey{}, phase)
				if native {
					op, err := s.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					result, err := s.Engine().Commit(op, ref)
					op.Done()
					if err != nil || !result.Committed {
						t.Fatal(result, err)
					}
				} else {
					if _, err := cursorUnitExecute(t, s, ctx, "COMMIT"); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 3 || cell.scope != nil || cell.expression != nil || c.authority != nil || c.source != nil {
					t.Fatal("commit retains scope/producer", calls, cell)
				}
			})
		}
	}
}
