package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestSQLCursorHoldOwner(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	c := PayloadOf(s).Cursors.Entries["h"]
	pin, cell, epoch := c.Source.View, CursorAuthority(c), CursorEpoch(c)
	first := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "COMMIT")
	if !CursorPromoted(c) || CursorTx(c) != (ids.TxRef{}) || CursorEpoch(c) != epoch || CursorRoot(c) != nil || c.Source != nil || CursorStage(c) != nil || CursorAuthority(c) != nil || CursorLeaves(c) != nil || CursorLocks(c) != nil || c.CurrentSource != nil || CursorState(c).Source != nil || CursorState(c).Parameters != nil || pin.Load() != nil || cell.Host != nil || cell.Expression != nil || CursorCount(c) != 2 || CursorStoreOffset(c) != 1 || c.OrdinalPosition != 1 {
		t.Fatal("export retains producer or loses owned remainder", c, cell)
	}
	if CursorCatalog(c).State() != nil || !reflect.DeepEqual(first.Rows.Schema(), CursorSchema(c)) {
		t.Fatal("export retains declaration catalog or loses owned schema")
	}
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE fresh CURSOR WITH HOLD FOR SELECT 9")
	sessionSQL(t, s, "ROLLBACK")
	if PayloadOf(s).Cursors.Entries["h"] != c || PayloadOf(s).Cursors.Entries["fresh"] != nil {
		t.Fatal("later rollback crosses exported ownership")
	}
	second := cursorUnitBatch(t, s, "FETCH 1 FROM h", [][]driver.Value{{int64(2)}})
	sessionSQL(t, s, "CLOSE h")
	if err := CursorDispose(c, context.Background()); err != nil {
		t.Fatal(err)
	}
	if CursorValues(c) != nil || CursorCatalog(c).State() != nil || cell.Host != nil || pin.Load() != nil {
		t.Fatal("repeated close retains resources")
	}
	if !reflect.DeepEqual(tuples(first), [][]driver.Value{{int64(1)}}) || !reflect.DeepEqual(tuples(second), [][]driver.Value{{int64(2)}}) {
		t.Fatal("close mutates detached batches")
	}
	sessionSQL(t, s, "DECLARE fenced CURSOR WITH HOLD FOR SELECT 7")
	if err := Reset(context.Background(), EngineOf(s)); err != nil {
		t.Fatal(err)
	}
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH ALL FROM fenced")
	if !errors.Is(err, ids.ErrCatalogReplaced) || result.Rows != nil {
		t.Fatal("export loses original epoch fence", result, err)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil || len(PayloadOf(s).Cursors.Entries) != 0 {
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
			c := PayloadOf(s).Cursors.Entries["h"]
			original := CursorRoot(c).Expressions[0]
			calls := 0
			CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
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
			if calls != 3 || !CursorPromoted(c) || CursorCount(c) != 3-min(fetched, 3) {
				t.Fatal("commit reopened/skipped producer", calls, CursorCount(c))
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
		z := PayloadOf(s).Cursors.Entries["z"]
		if CursorCount(z) != 3 || len(CursorSchema(z)) != 0 || len(CursorValues(z)) != 0 {
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
		c := PayloadOf(s).Cursors.Entries["h"]
		move := sessionSQL(t, s, "MOVE 2 FROM h")
		if move.Rows != nil || move.Count != 2 || CursorStage(c).Position != 2 || c.OrdinalPosition != 2 {
			t.Fatal("live MOVE and producer drain position diverge", move, c)
		}
		sessionSQL(t, s, "COMMIT")
		if CursorCount(c) != 1 || CursorStoreOffset(c) != 2 || c.OrdinalPosition != 2 {
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
			c := PayloadOf(s).Cursors.Entries["h"]
			pin, cell := c.Source.View, CursorAuthority(c)
			original, calls := CursorRoot(c).Expressions[0], 0
			CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
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
			if calls != min(fetched, 3)+3 || !CursorPromoted(c) || CursorCount(c) != 3 || CursorStoreOffset(c) != 0 || c.OrdinalPosition != ordinal || c.Place != position || c.AtEnd != end || CursorStage(c) != nil || CursorRoot(c) != nil || c.Source != nil || CursorAuthority(c) != nil || pin.Load() != nil || cell.Host != nil || cell.Expression != nil {
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
			if calls != min(fetched, 3)+3 || !reflect.DeepEqual(tuples(reverse), [][]driver.Value{{int64(3)}, {int64(2)}, {int64(1)}}) {
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
		c := PayloadOf(s).Cursors.Entries["e"]
		sessionSQL(t, s, "COMMIT")
		if !CursorPromoted(c) || CursorCount(c) != 0 || c.OrdinalPosition != 0 || !c.AtEnd || c.Place != exec.CursorAfter {
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
				c := PayloadOf(s).Cursors.Entries["h"]
				original, calls := CursorRoot(c).Expressions[0], 0
				CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
					calls++
					return original.Eval(ctx)
				})
				sessionSQL(t, s, fmt.Sprintf("MOVE %d FROM h", fetched))
				sessionSQL(t, s, "COMMIT")
				if calls != fetched+3 || CursorCount(c) != 3 || c.OrdinalPosition != int64(fetched) {
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
				c, ref := PayloadOf(s).Cursors.Entries["h"], s.Transactions.Ref
				cell := CursorAuthority(c)
				original := CursorRoot(c).Expressions[0]
				phase, calls := "fetch", 0
				var first *exec.CursorExpressionData
				CursorRoot(c).Expressions[0] = queryProbe(original.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
					scope := ScopeOf(ctx.LiveHost())
					if scope == nil || scope.Finished || scope.TxRef != ref || scope.LockTicket != s.Transactions.Ticket || ctx.Context.Value(cursorUnitContextKey{}) != phase || scope.Op.Released {
						return sqltype.Value{}, errors.New("producer borrows stale commit authority")
					}
					if !scope.Owner.Mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds transaction mutex")
					}
					scope.Owner.Mu.Unlock()
					if !EngineOf(s).Mu.TryLock() {
						return sqltype.Value{}, errors.New("projection holds engine mutex")
					}
					EngineOf(s).Mu.Unlock()
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
					result, err := EngineOf(s).Commit(op, ref)
					op.Done()
					if err != nil || !result.Committed {
						t.Fatal(result, err)
					}
				} else {
					if _, err := cursorUnitExecute(t, s, ctx, "COMMIT"); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 3 || cell.Host != nil || cell.Expression != nil || CursorAuthority(c) != nil || c.Source != nil {
					t.Fatal("commit retains scope/producer", calls, cell)
				}
			})
		}
	}
}
