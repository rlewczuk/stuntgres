package engine

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestSQLCursorScrollRetainedEOFAndMotion(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	c := s.cursors.entries["c"]
	first := cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	stage := c.stage
	if stage == nil || stage.closed || c.ordinalPosition != 3 || !c.atEnd || c.currentSource != nil {
		t.Fatal("EOF destroys retained tree or keeps lineage", c)
	}
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2)}})
	result, err := cursorUnitExecute(t, s, context.Background(), "MOVE BACKWARD ALL FROM c")
	if err != nil || result.Count != 1 || result.Rows != nil || c.position != cursorBefore || c.currentSource != nil || c.stage != stage || c.authority.scope != nil {
		t.Fatal(result, err, c)
	}
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "CLOSE c")
	if !stage.closed || c.source != nil || c.authority.expression != nil {
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
	c := s.cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2), int64(4)}})
	stage := c.stage
	if stage == nil || stage.position != 2 || !stage.retain || !c.state.virtualReads {
		t.Fatal("motion and virtual frame retention did not compose", c)
	}
	// Selected ordinal zero is present; the sequential frontier at ordinal
	// two is absent. Exercise the real engine without a public socket teardown.
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 3 FROM c", [][]driver.Value{{int64(3), nil}})
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{})
	if c.stage != stage || stage.closed || stage.position != 3 || !c.atEnd {
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
	if !stage.closed {
		t.Fatal("close retained generated frame", stage)
	}
}

func TestSQLCursorRetainedProducerReset(t *testing.T) {
	source := &querySource{singleton: true}
	child := indexedStage{source: source, selection: []int{}, done: true, position: 1}
	resets := 0
	child.reset = func(s *indexedStage) bool { resets++; s.selection = s.selection[:0]; s.done = false; return true }
	out := producingStage(&child, nil)
	out.selection = append(out.selection, 0)
	out.done = true
	out.position = 1
	if !out.rewind() || resets != 1 || child.position != 0 || out.position != 0 || out.done || len(out.selection) != 0 {
		t.Fatal("producer demand was not reset", out, child, resets)
	}
	// A materialized ORDER boundary replaces reset with position-only rewind.
	out.reset = nil
	out.selection = append(out.selection, 0)
	out.done = true
	if out.rewind() || resets != 1 || !out.done || len(out.selection) != 1 {
		t.Fatal("cached boundary invalidates child", out, resets)
	}
	t.Run("cached_parent_frontier", func(t *testing.T) {
		source := &querySource{materialized: true, flatCount: 3}
		child := indexedStage{source: source, position: 1}
		out := producingStage(&child, nil)
		out.selection = append(out.selection, 0)
		out.position = 1
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			i, present, err := child.nextOccurrence(ctx)
			if present && err == nil {
				retainOccurrence(out, &child, i)
			}
			return present, err
		}
		if out.rewind() || child.position != 1 || out.position != 0 || len(out.selection) != 1 {
			t.Fatal("cached rewind loses child demand frontier", out, child)
		}
		if err := out.drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if out.count() != 3 || out.index(0) != 0 || out.index(1) != 1 || out.index(2) != 2 || child.position != 3 {
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
	c := s.cursors.entries["c"]
	move := sessionSQL(t, s, "MOVE NEXT FROM c")
	source, stage := c.source, c.stage
	child := source.nestedStage
	if move.Count != 1 || move.Rows != nil || child == nil || child.position != 1 || source.flatCount != 1 || len(source.flat) != 1 {
		t.Fatal("partial view demand did not retain exactly one occurrence", move, source, child)
	}
	captured := source.flat[0]
	sessionSQL(t, s, "MOVE ABSOLUTE 0 FROM c")
	if c.stage != stage || source.nestedStage != child || child.position != 1 || source.flatCount != 1 || len(source.flat) != 1 || source.flat[0] != captured {
		t.Fatal("position-only rewind invalidates capture or loses child frontier", source, child)
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	if source.flatCount != 3 || len(source.flat) != 3 || child.position != 3 {
		t.Fatal("view drain appends an already-captured prefix", source, child)
	}
	sessionSQL(t, s, "MOVE ABSOLUTE 0 FROM c")
	if child.position != 3 || source.flatCount != 3 {
		t.Fatal("completed view rewind loses retained frontier", source, child)
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	if source.flatCount != 3 || child.position != 3 {
		t.Fatal("completed view replay demands duplicate occurrences", source, child)
	}
	sessionSQL(t, s, "CLOSE c")
	if !child.closed || !stage.closed {
		t.Fatal("view producer is not released on close", child, stage)
	}
}
