package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cursorPromotedUnit(t *testing.T, s *core.StatementScope, name, column string, first int64) *core.SQLCursor {
	t.Helper()
	integer, ok := s.Engine().CatalogRead().LookupType(sqltype.Int4)
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
	c := core.NewCursor(core.CursorFields{Epoch: s.TxRef.Epoch(), Promoted: true, Hold: true, Scroll: true, Schema: []catalog.Column{{Name: column, Usage: integer.Usage()}}, Values: []sqltype.Value{one, two}, Count: 2})
	core.PayloadOf(s.Op.Session).Cursors.Entries[name] = c
	return c
}

func TestPLpgSQLCursorPortalOwnership(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, sessionCompleted := range []bool{false, true} {
			for _, move := range []bool{false, true} {
				t.Run(fmt.Sprintf("promoted_flags/%t/%t/%t", completed, sessionCompleted, move), func(t *testing.T) {
					_, s := cursorUnitScope(t)
					c := cursorPromotedUnit(t, s, "held", "i", 7)
					s.Completed, *core.ScopeSessionCompletedRef(s) = completed, sessionCompleted
					motion := &parser.FetchCursor{Name: parser.Identifier{Text: "held"}, Count: 1, Move: move}
					result, err := core.ScopeFetchCursor(s, motion)
					if err != nil {
						t.Fatal(err)
					}
					if result.Count != 1 || c.OrdinalPosition != 1 || s.Completed != completed || core.ScopeSessionCompleted(s) != sessionCompleted {
						t.Fatal("lower held motion completed its parent", result.Count, c.OrdinalPosition, s.Completed, core.ScopeSessionCompleted(s))
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
					result, err = core.ScopeExecuteFetchCursor(s, motion, 0)
					if err != nil {
						t.Fatal(err)
					}
					if result.Rows != nil {
						defer result.Rows.Close()
					}
					if result.Count != 1 || !s.Completed || !core.ScopeSessionCompleted(s) {
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
		if err := core.ScopeMovePLCursorPortal(s, core.ScopeNewPLRun(s, nil, frame, read), frame, []proc.PLTarget{{Slot: 2}}, original, motion, read); err != nil {
			t.Fatal(err)
		}
		row := frame.Values[2]
		if row.Type().CompositeMember(0).Name != "original_field" || core.CursorRecordShape(original) != row.Type() || core.CursorRecordShape(other) != nil || other.OrdinalPosition != 0 {
			t.Fatal("implicit movement followed mutable slot or descriptor")
		}
		if n, err := row.CompositeField(0).Integer(); err != nil || n != 7 {
			t.Fatal(n, err)
		}
		core.PayloadOf(s.Op.Session).Cursors.Entries["original"] = other
		_, err := core.ScopeFetchCursorPortal(s, original, motion)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "34000" || original.OrdinalPosition != 1 || other.OrdinalPosition != 0 {
			t.Fatal("same-name replacement redirected retained portal", err)
		}
		core.PayloadOf(s.Op.Session).Cursors.Entries["original"] = original
	})
	for _, assignment := range []string{"'missing'", "NULL", "'other'"} {
		t.Run("loop_retains/"+assignment, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			other := cursorPromotedUnit(t, s, "other", "different_field", 100)
			d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR VALUES(10),(20),(30); n integer:=0; BEGIN c:='owned_identity'; FOR r IN c LOOP n:=n+r.column1; c:="+assignment+"; END LOOP; RETURN n; END")
			frame := proc.NewFrame(nil)
			defer frame.Close()
			value, err := core.ScopeInvokePLRoutine(s, d, frame, e.CatalogRead(), sqltype.CollationRef{})
			if err != nil {
				t.Fatal(err)
			}
			if n, err := value.Integer(); err != nil || n != 60 {
				t.Fatal(n, err)
			}
			if core.PayloadOf(s.Op.Session).Cursors.Entries["owned_identity"] != nil || other.OrdinalPosition != 0 {
				t.Fatal("loop leaked original or moved another portal")
			}
		})
	}
	t.Run("pinned_drop_and_forced_cleanup", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		c := cursorPromotedUnit(t, s, "pinned", "i", 7)
		core.SetCursorPinned(c, true)
		calls := 0
		core.SetCursorStage(c, &exec.Stage{Finish: func() error {
			calls++
			if core.CursorPinned(c) || core.PayloadOf(s.Op.Session).Cursors.Entries["pinned"] != nil {
				t.Error("cleanup before unpin/detach")
			}
			if !e.Mu.TryLock() {
				t.Error("cleanup under engine mutex")
			} else {
				e.Mu.Unlock()
			}
			if !s.Owner.Mu.TryLock() {
				t.Error("cleanup under transaction mutex")
			} else {
				s.Owner.Mu.Unlock()
			}
			if !s.Op.Session.Mu.TryLock() {
				t.Error("cleanup under session mutex")
			} else {
				s.Op.Session.Mu.Unlock()
			}
			return nil
		}})
		priorReplacement := s.ReplacementRead
		for _, all := range []bool{false, true} {
			_, err := core.ScopeCloseCursor(s, &parser.CloseCursor{Name: parser.Identifier{Text: "pinned"}, All: all})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "24000" || diagnostic.Message != "cannot drop pinned portal \"pinned\"" {
				t.Fatal(err)
			}
			if core.PayloadOf(s.Op.Session).Cursors.Entries["pinned"] != c || !core.CursorPinned(c) || calls != 0 || s.ReplacementRead != priorReplacement {
				t.Fatal("rejected drop changed owner state")
			}
		}
		// Pinning protects user drop only, not explicit shared motion.
		if _, err := core.ScopeFetchCursor(s, &parser.FetchCursor{Name: parser.Identifier{Text: "pinned"}, Move: true, Count: 1}); err != nil {
			t.Fatal(err)
		}
		if !core.CursorPinned(c) || c.OrdinalPosition != 1 {
			t.Fatal("movement changed protection")
		}
		if err := core.DisposeSQLCursors(context.Background(), core.CursorsClear(core.PayloadOf(s.Op.Session).Cursors)); err != nil {
			t.Fatal(err)
		}
		if err := core.CursorDispose(c, context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || core.CursorPinned(c) {
			t.Fatal("forced cleanup was not once/unpinned", calls)
		}
	})
	t.Run("pinned_body_error_unwinds", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR SELECT 7 AS i; BEGIN c:='owned_pin'; FOR r IN c LOOP CLOSE c; RETURN r.i; END LOOP; RETURN 0; END")
		frame := proc.NewFrame(nil)
		defer frame.Close()
		_, err := core.ScopeInvokePLRoutine(s, d, frame, e.CatalogRead(), sqltype.CollationRef{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "24000" {
			t.Fatal(err)
		}
		if core.PayloadOf(s.Op.Session).Cursors.Entries["owned_pin"] != nil || s.Completed || core.ScopeSessionCompleted(s) {
			t.Fatal("pinned loop error retained portal/completed parent")
		}
	})
}
