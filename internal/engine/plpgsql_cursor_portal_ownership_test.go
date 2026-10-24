package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cursorPromotedUnit(t *testing.T, s *StatementScope, name, column string, first int64) *sqlCursor {
	t.Helper()
	integer, ok := s.engine().CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing integer")
	}
	one, err := sqltype.Integer(integer, first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(integer, first+1)
	if err != nil {
		t.Fatal(err)
	}
	c := &sqlCursor{epoch: s.txRef.Epoch(), promoted: true, hold: true, scroll: true, schema: []catalog.Column{{Name: column, Usage: integer.Usage()}}, values: []sqltype.Value{one, two}, count: 2}
	payloadOf(s.op.Session).cursors.entries[name] = c
	return c
}

func TestPLpgSQLCursorPortalOwnership(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, sessionCompleted := range []bool{false, true} {
			for _, move := range []bool{false, true} {
				t.Run(fmt.Sprintf("promoted_flags/%t/%t/%t", completed, sessionCompleted, move), func(t *testing.T) {
					_, s := cursorUnitScope(t)
					c := cursorPromotedUnit(t, s, "held", "i", 7)
					s.completed, s.sessionCompleted = completed, sessionCompleted
					motion := &parser.FetchCursor{Name: parser.Identifier{Text: "held"}, Count: 1, Move: move}
					result, err := s.fetchCursor(motion)
					if err != nil {
						t.Fatal(err)
					}
					if result.Count != 1 || c.OrdinalPosition != 1 || s.completed != completed || s.sessionCompleted != sessionCompleted {
						t.Fatal("lower held motion completed its parent", result.Count, c.OrdinalPosition, s.completed, s.sessionCompleted)
					}
					if move {
						if result.Rows != nil {
							t.Fatal("MOVE materialized a row result")
						}
					} else {
						rows, ok := result.Rows.(*exec.FinalRows)
						if !ok || rows.Count != 1 || len(rows.Values) != 1 {
							t.Fatal("lost typed held result")
						}
						if n, err := rows.Values[0].Integer(); err != nil || n != 7 {
							t.Fatal(n, err)
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
					}
					result, err = s.executeFetchCursor(motion, 0)
					if err != nil {
						t.Fatal(err)
					}
					if result.Rows != nil {
						defer result.Rows.Close()
					}
					if result.Count != 1 || !s.completed || !s.sessionCompleted {
						t.Fatal("SQL held wrapper lost completion", result.Count)
					}
				})
			}
		}
	}
	t.Run("retained_descriptor_and_substitution", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		original := cursorPromotedUnit(t, s, "original", "original_field", 7)
		other := cursorPromotedUnit(t, s, "other", "different_field", 100)
		read := e.CatalogRead()
		boolean, _ := read.LookupType(sqltype.Bool)
		refcursor, _ := read.LookupType(sqltype.Refcursor)
		frame := &proc.PLFrame{Program: &proc.PLProgram{FoundSlot: 0, Variables: []proc.PLVariable{{Usage: boolean.Usage(), Role: proc.PLFoundSlot}, {Usage: refcursor.Usage()}, {Record: true}}}, Values: []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.String(refcursor, "other"), {}}}
		defer frame.Close()
		motion := &parser.FetchCursor{Name: parser.Identifier{Text: "original"}, Count: 1}
		if err := s.movePLCursorPortal(s.newPLRun(nil, frame, read), frame, []proc.PLTarget{{Slot: 2}}, original, motion, read); err != nil {
			t.Fatal(err)
		}
		row := frame.Values[2]
		if row.Type().CompositeMember(0).Name != "original_field" || original.recordShape != row.Type() || other.recordShape != nil || other.OrdinalPosition != 0 {
			t.Fatal("implicit movement followed mutable slot or descriptor")
		}
		if n, err := row.CompositeField(0).Integer(); err != nil || n != 7 {
			t.Fatal(n, err)
		}
		payloadOf(s.op.Session).cursors.entries["original"] = other
		_, err := s.fetchCursorPortal(original, motion)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "34000" || original.OrdinalPosition != 1 || other.OrdinalPosition != 0 {
			t.Fatal("same-name replacement redirected retained portal", err)
		}
		payloadOf(s.op.Session).cursors.entries["original"] = original
	})
	for _, assignment := range []string{"'missing'", "NULL", "'other'"} {
		t.Run("loop_retains/"+assignment, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			other := cursorPromotedUnit(t, s, "other", "different_field", 100)
			d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR VALUES(10),(20),(30); n integer:=0; BEGIN c:='owned_identity'; FOR r IN c LOOP n:=n+r.column1; c:="+assignment+"; END LOOP; RETURN n; END")
			frame := proc.NewFrame(nil)
			defer frame.Close()
			value, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			if err != nil {
				t.Fatal(err)
			}
			if n, err := value.Integer(); err != nil || n != 60 {
				t.Fatal(n, err)
			}
			if payloadOf(s.op.Session).cursors.entries["owned_identity"] != nil || other.OrdinalPosition != 0 {
				t.Fatal("loop leaked original or moved another portal")
			}
		})
	}
	t.Run("pinned_drop_and_forced_cleanup", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		c := cursorPromotedUnit(t, s, "pinned", "i", 7)
		c.pinned = true
		calls := 0
		c.stage = &exec.Stage{Finish: func() error {
			calls++
			if c.pinned || payloadOf(s.op.Session).cursors.entries["pinned"] != nil {
				t.Error("cleanup before unpin/detach")
			}
			if !e.mu.TryLock() {
				t.Error("cleanup under engine mutex")
			} else {
				e.mu.Unlock()
			}
			if !s.owner.Mu.TryLock() {
				t.Error("cleanup under transaction mutex")
			} else {
				s.owner.Mu.Unlock()
			}
			if !s.op.Session.Mu.TryLock() {
				t.Error("cleanup under session mutex")
			} else {
				s.op.Session.Mu.Unlock()
			}
			return nil
		}}
		priorReplacement := s.replacementRead
		for _, all := range []bool{false, true} {
			_, err := s.closeCursor(&parser.CloseCursor{Name: parser.Identifier{Text: "pinned"}, All: all})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "24000" || diagnostic.Message != "cannot drop pinned portal \"pinned\"" {
				t.Fatal(err)
			}
			if payloadOf(s.op.Session).cursors.entries["pinned"] != c || !c.pinned || calls != 0 || s.replacementRead != priorReplacement {
				t.Fatal("rejected drop changed owner state")
			}
		}
		// Pinning protects user drop only, not explicit shared motion.
		if _, err := s.fetchCursor(&parser.FetchCursor{Name: parser.Identifier{Text: "pinned"}, Move: true, Count: 1}); err != nil {
			t.Fatal(err)
		}
		if !c.pinned || c.OrdinalPosition != 1 {
			t.Fatal("movement changed protection")
		}
		if err := disposeSQLCursors(context.Background(), payloadOf(s.op.Session).cursors.clear()); err != nil {
			t.Fatal(err)
		}
		if err := c.dispose(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || c.pinned {
			t.Fatal("forced cleanup was not once/unpinned", calls)
		}
	})
	t.Run("pinned_body_error_unwinds", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR SELECT 7 AS i; BEGIN c:='owned_pin'; FOR r IN c LOOP CLOSE c; RETURN r.i; END LOOP; RETURN 0; END")
		frame := proc.NewFrame(nil)
		defer frame.Close()
		_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "24000" {
			t.Fatal(err)
		}
		if payloadOf(s.op.Session).cursors.entries["owned_pin"] != nil || s.completed || s.sessionCompleted {
			t.Fatal("pinned loop error retained portal/completed parent")
		}
	})
}
