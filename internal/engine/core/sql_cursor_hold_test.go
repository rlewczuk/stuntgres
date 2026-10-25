package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestSQLCursorHoldOwner(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	c := core.PayloadOf(s).Cursors.Entries["h"]
	pin, cell, epoch := c.Source.View, core.CursorAuthority(c), core.CursorEpoch(c)
	first := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "COMMIT")
	if !core.CursorPromoted(c) || core.CursorTx(c) != (ids.TxRef{}) || core.CursorEpoch(c) != epoch || core.CursorRoot(c) != nil || c.Source != nil || core.CursorStage(c) != nil || core.CursorAuthority(c) != nil || core.CursorLeaves(c) != nil || core.CursorLocks(c) != nil || c.CurrentSource != nil || core.CursorState(c).Source != nil || core.CursorState(c).Parameters != nil || pin.Load() != nil || cell.Host != nil || cell.Expression != nil || core.CursorCount(c) != 2 || core.CursorStoreOffset(c) != 1 || c.OrdinalPosition != 1 {
		t.Fatal("export retains producer or loses owned remainder", c, cell)
	}
	if core.CursorCatalog(c).State() != nil || !reflect.DeepEqual(first.Rows.Schema(), core.CursorSchema(c)) {
		t.Fatal("export retains declaration catalog or loses owned schema")
	}
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE fresh CURSOR WITH HOLD FOR SELECT 9")
	sessionSQL(t, s, "ROLLBACK")
	if core.PayloadOf(s).Cursors.Entries["h"] != c || core.PayloadOf(s).Cursors.Entries["fresh"] != nil {
		t.Fatal("later rollback crosses exported ownership")
	}
	second := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(2)}})
	sessionSQL(t, s, "CLOSE h")
	if err := core.CursorDispose(c, context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.CursorValues(c) != nil || core.CursorCatalog(c).State() != nil || cell.Host != nil || pin.Load() != nil {
		t.Fatal("repeated close retains resources")
	}
	if !reflect.DeepEqual(enginetest.Tuples(first), [][]driver.Value{{int64(1)}}) || !reflect.DeepEqual(enginetest.Tuples(second), [][]driver.Value{{int64(2)}}) {
		t.Fatal("close mutates detached batches")
	}
	sessionSQL(t, s, "DECLARE fenced CURSOR WITH HOLD FOR SELECT 7")
	if err := engine.Reset(context.Background(), core.EngineOf(s)); err != nil {
		t.Fatal(err)
	}
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH ALL FROM fenced")
	if !errors.Is(err, ids.ErrCatalogReplaced) || result.Rows != nil {
		t.Fatal("export loses original epoch fence", result, err)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil || len(core.PayloadOf(s).Cursors.Entries) != 0 {
		t.Fatal("physical end retains store", err)
	}
	if !reflect.DeepEqual(enginetest.Tuples(second), [][]driver.Value{{int64(2)}}) {
		t.Fatal("physical end mutates detached batch")
	}
}

func TestSQLCursorHoldDemand(t *testing.T) {
	for _, fetched := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(fetched), func(t *testing.T) {
			s := cursorUnitSetup(t)
			sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
			c := core.PayloadOf(s).Cursors.Entries["h"]
			original := core.CursorRoot(c).Expressions[0]
			calls := 0
			core.CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
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
			if calls != 3 || !core.CursorPromoted(c) || core.CursorCount(c) != 3-min(fetched, 3) {
				t.Fatal("commit reopened/skipped producer", calls, core.CursorCount(c))
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
		z := core.PayloadOf(s).Cursors.Entries["z"]
		if core.CursorCount(z) != 3 || len(core.CursorSchema(z)) != 0 || len(core.CursorValues(z)) != 0 {
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
	t.Run("move_position_drain", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
		c := core.PayloadOf(s).Cursors.Entries["h"]
		move := sessionSQL(t, s, "MOVE 2 FROM h")
		if move.Rows != nil || move.Count != 2 || core.CursorStage(c).Position != 2 || c.OrdinalPosition != 2 {
			t.Fatal("live MOVE and producer drain position diverge", move, c)
		}
		sessionSQL(t, s, "COMMIT")
		if core.CursorCount(c) != 1 || core.CursorStoreOffset(c) != 2 || c.OrdinalPosition != 2 {
			t.Fatal("NO SCROLL export is not remaining-only", c)
		}
		cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{{int64(3)}})
	})
}

func TestSQLCursorScrollHoldOwner(t *testing.T) {
	for _, fetched := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(fetched), func(t *testing.T) {
			s := cursorUnitSetup(t)
			sessionSQL(t, s, "DECLARE h SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
			c := core.PayloadOf(s).Cursors.Entries["h"]
			pin, cell := c.Source.View, core.CursorAuthority(c)
			original, calls := core.CursorRoot(c).Expressions[0], 0
			core.CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
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
			ordinal, position, end := c.OrdinalPosition, c.Place, c.AtEnd
			sessionSQL(t, s, "COMMIT")
			if calls != min(fetched, 3)+3 || !core.CursorPromoted(c) || core.CursorCount(c) != 3 || core.CursorStoreOffset(c) != 0 || c.OrdinalPosition != ordinal || c.Place != position || c.AtEnd != end || core.CursorStage(c) != nil || core.CursorRoot(c) != nil || c.Source != nil || core.CursorAuthority(c) != nil || pin.Load() != nil || cell.Host != nil || cell.Expression != nil {
				t.Fatal("SCROLL promotion loses position/full store or retains producer", calls, c)
			}
			sessionSQL(t, s, "MOVE FORWARD ALL FROM h")
			reverse := cursorUnitBatch(t, s, "FETCH BACKWARD ALL FROM h", [][]driver.Value{{int64(3)}, {int64(2)}, {int64(1)}})
			move := sessionSQL(t, s, "MOVE ABSOLUTE 2 FROM h")
			if move.Rows != nil || move.Count != 1 {
				t.Fatal("stored MOVE publishes rows or wrong count", move)
			}
			cursorUnitBatch(t, s, "FETCH RELATIVE 0 FROM h", [][]driver.Value{{int64(2)}})
			sessionSQL(t, s, "CLOSE h")
			if calls != min(fetched, 3)+3 || !reflect.DeepEqual(enginetest.Tuples(reverse), [][]driver.Value{{int64(3)}, {int64(2)}, {int64(1)}}) {
				t.Fatal("stored motion reprojects or mutates detached reverse batch", calls)
			}
		})
	}
	t.Run("zero_width", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE z SCROLL CURSOR WITH HOLD FOR SELECT FROM unit_rows")
		sessionSQL(t, s, "MOVE 2 FROM z")
		sessionSQL(t, s, "COMMIT")
		cursorUnitBatch(t, s, "FETCH PRIOR FROM z", [][]driver.Value{{}})
		move := sessionSQL(t, s, "MOVE FORWARD ALL FROM z")
		if move.Rows != nil || move.Count != 2 {
			t.Fatal("stored zero-width MOVE loses cardinality", move)
		}
		cursorUnitBatch(t, s, "FETCH BACKWARD ALL FROM z", [][]driver.Value{{}, {}, {}})
	})
	t.Run("empty_eof", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE e SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows WHERE FALSE")
		cursorUnitBatch(t, s, "FETCH ALL FROM e", [][]driver.Value{})
		c := core.PayloadOf(s).Cursors.Entries["e"]
		sessionSQL(t, s, "COMMIT")
		if !core.CursorPromoted(c) || core.CursorCount(c) != 0 || c.OrdinalPosition != 0 || !c.AtEnd || c.Place != exec.CursorAfter {
			t.Fatal("empty SCROLL export loses attempted EOF", c)
		}
		empty := cursorUnitBatch(t, s, "FETCH PRIOR FROM e", [][]driver.Value{})
		if len(empty.Rows.Schema()) != 1 || empty.Rows.Schema()[0].Name != "i" {
			t.Fatal("empty reverse store loses descriptors")
		}
		move := sessionSQL(t, s, "MOVE 0 FROM e")
		if move.Rows != nil || move.Count != 0 {
			t.Fatal("empty stored MOVE publishes rows/count", move)
		}
	})
	t.Run("derived_cached_frontier", func(t *testing.T) {
		for _, fetched := range []int{1, 3} {
			t.Run(fmt.Sprint(fetched), func(t *testing.T) {
				s := cursorUnitSetup(t)
				sessionSQL(t, s, "DECLARE h SCROLL CURSOR WITH HOLD FOR SELECT i FROM (SELECT i FROM unit_rows ORDER BY i OFFSET 0) AS ordered")
				c := core.PayloadOf(s).Cursors.Entries["h"]
				original, calls := core.CursorRoot(c).Expressions[0], 0
				core.CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
					calls++
					return original.Eval(ctx)
				})
				sessionSQL(t, s, fmt.Sprintf("MOVE %d FROM h", fetched))
				sessionSQL(t, s, "COMMIT")
				if calls != fetched+3 || core.CursorCount(c) != 3 || c.OrdinalPosition != int64(fetched) {
					t.Fatal("derived rewind duplicates captured prefix during full export", calls, c)
				}
				cursorUnitBatch(t, s, "FETCH ABSOLUTE -1 FROM h", [][]driver.Value{{int64(3)}})
				cursorUnitBatch(t, s, "FETCH BACKWARD ALL FROM h", [][]driver.Value{{int64(2)}, {int64(1)}})
				if calls != fetched+3 {
					t.Fatal("derived store replays projection", calls)
				}
			})
		}
	})
}

func TestSQLCursorHoldAuthority(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, before := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/prior=%t", native, before), func(t *testing.T) {
				s := cursorUnitSetup(t)
				sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
				c, ref := core.PayloadOf(s).Cursors.Entries["h"], s.Transactions.Ref
				cell := core.CursorAuthority(c)
				original := core.CursorRoot(c).Expressions[0]
				phase, calls := "fetch", 0
				var first *exec.CursorExpressionData
				core.CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
					scope := core.ScopeOf(ctx.LiveHost())
					if scope == nil || scope.Finished || scope.TxRef != ref || scope.LockTicket != s.Transactions.Ticket || ctx.Context.Value(cursorUnitContextKey{}) != phase || scope.Op.Released {
						return sqltype.Value{}, errors.New("producer borrows stale commit authority")
					}
					if !scope.Owner.Mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds transaction mutex")
					}
					scope.Owner.Mu.Unlock()
					if !core.EngineOf(s).Mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds engine mutex")
					}
					core.EngineOf(s).Mu.Unlock()
					if !s.Mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds session mutex")
					}
					s.Mu.Unlock()
					if first == nil {
						first = cell.Expression
					} else if cell.Expression != first {
						return sqltype.Value{}, errors.New("commit replaces first-demand expression data")
					}
					if first == nil || first.StatementStart.IsZero() {
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
					result, err := core.EngineOf(s).Commit(op, ref)
					op.Done()
					if err != nil || !result.Committed {
						t.Fatal(result, err)
					}
				} else {
					if _, err := cursorUnitExecute(t, s, ctx, "COMMIT"); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 3 || cell.Host != nil || cell.Expression != nil || core.CursorAuthority(c) != nil || c.Source != nil {
					t.Fatal("commit retains scope/producer", calls, cell)
				}
			})
		}
	}
}
