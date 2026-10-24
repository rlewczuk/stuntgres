package engine

import (
	"context"
	"database/sql/driver"
	"stuntgres/internal/engine/exec"
	"testing"
)

func TestSQLCursorScrollRetainedEOFAndMotion(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	c := payloadOf(s).cursors.entries["c"]
	first := cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	stage := c.stage
	if stage == nil || stage.Closed || c.OrdinalPosition != 3 || !c.AtEnd || c.CurrentSource != nil {
		t.Fatal("EOF destroys retained tree or keeps lineage", c)
	}
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2)}})
	result, err := cursorUnitExecute(t, s, context.Background(), "MOVE BACKWARD ALL FROM c")
	if err != nil || result.Count != 1 || result.Rows != nil || c.Place != cursorBefore || c.CurrentSource != nil || c.stage != stage || c.authority.Host != nil {
		t.Fatal(result, err, c)
	}
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "CLOSE c")
	if !stage.Closed || c.source != nil || c.authority.Expression != nil {
		t.Fatal("close leaks retained tree", c)
	}
	if first.Count != 3 {
		t.Fatal("motion changes detached batch", first)
	}
}

func TestSQLCursorScrollVirtualFrameIdentity(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "COMMIT")
	sessionSQL(t, s, "CREATE TABLE unit_virtual_identity(i INTEGER NOT NULL,n INTEGER GENERATED ALWAYS AS(i+1) VIRTUAL)")
	sessionSQL(t, s, "INSERT INTO unit_virtual_identity(i) VALUES(1),(2)")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT l.i,g.n+1 AS n FROM unit_rows AS l LEFT JOIN unit_virtual_identity AS g ON l.i=g.i ORDER BY l.i LIMIT 3 OFFSET 0")
	c := payloadOf(s).cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2), int64(4)}})
	stage := c.stage
	if stage == nil || stage.Position != 2 || !stage.Retain || !c.state.VirtualReads {
		t.Fatal("motion and virtual frame retention did not compose", c)
	}
	// Selected ordinal zero is present; the sequential frontier at ordinal
	// two is absent. Exercise the real engine without a public socket teardown.
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 3 FROM c", [][]driver.Value{{int64(3), nil}})
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{})
	if c.stage != stage || stage.Closed || stage.Position != 3 || !c.AtEnd {
		t.Fatal("EOF lost retained frame or frontier", c)
	}
	// At EOF the sequential frontier is outside the permutation. Identity
	// binding must index the selected occurrence, not that frontier.
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(3), nil}})
	cursorUnitBatch(t, s, "FETCH RELATIVE -2 FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 0 FROM c", [][]driver.Value{})
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1), int64(3)}, {int64(2), int64(4)}, {int64(3), nil}})
	cursorUnitBatch(t, s, "FETCH BACKWARD ALL FROM c", [][]driver.Value{{int64(3), nil}, {int64(2), int64(4)}, {int64(1), int64(3)}})
	sessionSQL(t, s, "CLOSE c")
	if !stage.Closed {
		t.Fatal("close retained generated frame", stage)
	}
}

func TestSQLCursorRetainedProducerReset(t *testing.T) {
	source := &exec.Source{Singleton: true}
	child := exec.Stage{Source: source, Selection: []int{}, Done: true, Position: 1}
	resets := 0
	child.Reset = func(ctx context.Context, s *exec.Stage) (bool, error) {
		resets++
		s.Selection = s.Selection[:0]
		s.Done = false
		return true, nil
	}
	out := exec.ProducingStage(&child, nil)
	out.Selection = append(out.Selection, 0)
	out.Done = true
	out.Position = 1
	if changed, err := out.Rewind(context.Background()); err != nil || !changed || resets != 1 || child.Position != 0 || out.Position != 0 || out.Done || len(out.Selection) != 0 {
		t.Fatal("producer demand was not reset", out, child, resets)
	}
	// A materialized ORDER boundary replaces reset with position-only rewind.
	out.Reset = nil
	out.Selection = append(out.Selection, 0)
	out.Done = true
	if changed, err := out.Rewind(context.Background()); err != nil || changed || resets != 1 || !out.Done || len(out.Selection) != 1 {
		t.Fatal("cached boundary invalidates child", out, resets)
	}
	t.Run("cached_parent_frontier", func(t *testing.T) {
		source := &exec.Source{Materialized: true, FlatCount: 3}
		child := exec.Stage{Source: source, Position: 1}
		out := exec.ProducingStage(&child, nil)
		out.Selection = append(out.Selection, 0)
		out.Position = 1
		out.Produce = func(ctx context.Context, out *exec.Stage) (bool, error) {
			i, present, err := child.NextOccurrence(ctx)
			if present && err == nil {
				exec.RetainOccurrence(out, &child, i)
			}
			return present, err
		}
		if changed, err := out.Rewind(context.Background()); err != nil || changed || child.Position != 1 || out.Position != 0 || len(out.Selection) != 1 {
			t.Fatal("cached rewind loses child demand frontier", out, child)
		}
		if err := out.Drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if out.Count() != 3 || out.Index(0) != 0 || out.Index(1) != 1 || out.Index(2) != 2 || child.Position != 3 {
			t.Fatal("cached rewind appends captured prefix again", out, child)
		}
	})
}

func TestSQLCursorStoredViewFrontier(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "COMMIT")
	sessionSQL(t, s, "CREATE VIEW unit_cached_view AS SELECT i FROM unit_rows ORDER BY i")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_cached_view")
	c := payloadOf(s).cursors.entries["c"]
	move := sessionSQL(t, s, "MOVE NEXT FROM c")
	source, stage := c.source, c.stage
	child := source.NestedStage
	if move.Count != 1 || move.Rows != nil || child == nil || child.Position != 1 || source.FlatCount != 1 || len(source.Flat) != 1 {
		t.Fatal("partial view demand did not retain exactly one occurrence", move, source, child)
	}
	captured := source.Flat[0]
	sessionSQL(t, s, "MOVE ABSOLUTE 0 FROM c")
	if c.stage != stage || source.NestedStage != child || child.Position != 1 || source.FlatCount != 1 || len(source.Flat) != 1 || source.Flat[0] != captured {
		t.Fatal("position-only rewind invalidates capture or loses child frontier", source, child)
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	if source.FlatCount != 3 || len(source.Flat) != 3 || child.Position != 3 {
		t.Fatal("view drain appends an already-captured prefix", source, child)
	}
	sessionSQL(t, s, "MOVE ABSOLUTE 0 FROM c")
	if child.Position != 3 || source.FlatCount != 3 {
		t.Fatal("completed view rewind loses retained frontier", source, child)
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	if source.FlatCount != 3 || child.Position != 3 {
		t.Fatal("completed view replay demands duplicate occurrences", source, child)
	}
	sessionSQL(t, s, "CLOSE c")
	if !child.Closed || !stage.Closed {
		t.Fatal("view producer is not released on close", child, stage)
	}
}
