package core_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
)

func TestSQLCursorScrollRetainedEOFAndMotion(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	c := core.PayloadOf(s).Cursors.Entries["c"]
	first := cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
	stage := core.CursorStage(c)
	if stage == nil || stage.Closed || c.OrdinalPosition != 3 || !c.AtEnd || c.CurrentSource != nil {
		t.Fatal("EOF destroys retained tree or keeps lineage", c)
	}
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2)}})
	result, err := cursorUnitExecute(t, s, context.Background(), "MOVE BACKWARD ALL FROM c")
	if err != nil || result.Count != 1 || result.Rows != nil || c.Place != exec.CursorBefore || c.CurrentSource != nil || core.CursorStage(c) != stage || core.CursorAuthority(c).Host != nil {
		t.Fatal(result, err, c)
	}
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "CLOSE c")
	if !stage.Closed || c.Source != nil || core.CursorAuthority(c).Expression != nil {
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
	c := core.PayloadOf(s).Cursors.Entries["c"]
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 2 FROM c", [][]driver.Value{{int64(2), int64(4)}})
	stage := core.CursorStage(c)
	if stage == nil || stage.Position != 2 || !stage.Retain || !core.CursorState(c).IdentityReads {
		t.Fatal("motion and virtual frame retention did not compose", c)
	}
	// Selected ordinal zero is present; the sequential frontier at ordinal
	// two is absent. Exercise the real engine without a public socket teardown.
	cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(1), int64(3)}})
	cursorUnitBatch(t, s, "FETCH ABSOLUTE 3 FROM c", [][]driver.Value{{int64(3), nil}})
	cursorUnitBatch(t, s, "FETCH NEXT FROM c", [][]driver.Value{})
	if core.CursorStage(c) != stage || stage.Closed || stage.Position != 3 || !c.AtEnd {
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

func TestSQLCursorStoredViewFrontier(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "COMMIT")
	sessionSQL(t, s, "CREATE VIEW unit_cached_view AS SELECT i FROM unit_rows ORDER BY i")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_cached_view")
	c := core.PayloadOf(s).Cursors.Entries["c"]
	move := sessionSQL(t, s, "MOVE NEXT FROM c")
	source, stage := c.Source, core.CursorStage(c)
	child := source.NestedStage
	if move.Count != 1 || move.Rows != nil || child == nil || child.Position != 1 || source.FlatCount != 1 || len(source.Flat) != 1 {
		t.Fatal("partial view demand did not retain exactly one occurrence", move, source, child)
	}
	captured := source.Flat[0]
	sessionSQL(t, s, "MOVE ABSOLUTE 0 FROM c")
	if core.CursorStage(c) != stage || source.NestedStage != child || child.Position != 1 || source.FlatCount != 1 || len(source.Flat) != 1 || source.Flat[0] != captured {
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
