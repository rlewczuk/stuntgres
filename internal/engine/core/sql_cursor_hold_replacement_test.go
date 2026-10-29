package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLCursorHoldReplacement(t *testing.T) {
	s := cursorUnitSetup(t)
	e := core.EngineOf(s)
	sessionSQL(t, s, "DECLARE retained NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	c := core.PayloadOf(s).Cursors.Entries["retained"]
	pin, authority := c.Source.View, core.CursorAuthority(c)
	cursorUnitBatch(t, s, "FETCH 1 FROM retained", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "COMMIT")
	values, schema := core.CursorValues(c), core.CursorSchema(c)
	owned := append([]sqltype.Value(nil), values...)
	epoch := core.CursorEpoch(c)
	if len(values) != 2 || len(schema) != 1 || !core.CursorPromoted(c) || core.CursorTx(c) != (ids.TxRef{}) || c.Source != nil || core.CursorRoot(c) != nil || core.CursorAuthority(c) != nil || core.CursorStage(c) != nil || pin.Load() != nil || authority.Host != nil {
		t.Fatal("promotion retained producer authority or lost owned store")
	}
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if core.PayloadOf(s).Cursors.Entries["retained"] != c || &core.CursorValues(c)[0] != &values[0] || &core.CursorSchema(c)[0] != &schema[0] || !reflect.DeepEqual(core.CursorValues(c), owned) || core.CursorEpoch(c) != epoch {
		t.Fatal("replacement rebuilt, retargeted or mutated retained output")
	}
	cursorUnitBatch(t, s, "FETCH 1 FROM retained", [][]driver.Value{{int64(2)}})
	if &core.CursorValues(c)[0] != &values[0] || !reflect.DeepEqual(core.CursorValues(c), owned) || core.CursorAuthority(c) != nil || pin.Load() != nil {
		t.Fatal("fresh FETCH borrowed producer authority or changed backing")
	}

	// A materialized store confers neither a name-rebinding capability nor a
	// cross-session portal capability, even when its original epoch is old.
	peer := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, peer)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.ScopeFetchCursorPortal(scope, c, &parser.FetchCursor{Name: parser.Identifier{Text: "retained"}, Count: 1})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "34000" {
		t.Fatal("foreign session accepted held output", err)
	}
	scope.Finish(err)
	op.Done()

	sessionSQL(t, s, "DECLARE replacement CURSOR WITH HOLD FOR SELECT 99")
	replacement := core.PayloadOf(s).Cursors.Entries["replacement"]
	registry := core.PayloadOf(s).Cursors
	registry.Entries["retained"] = replacement
	op = enginetest.TransactionOperation(t, s)
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.ScopeFetchCursorPortal(scope, c, &parser.FetchCursor{Name: parser.Identifier{Text: "retained"}, Count: 1})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "34000" {
		t.Fatal("same name redirected old portal", err)
	}
	scope.Finish(err)
	op.Done()
	registry.Entries["retained"] = c
	cursorUnitBatch(t, s, "FETCH ALL FROM retained", [][]driver.Value{{int64(3)}})
	cursorUnitBatch(t, s, "FETCH ALL FROM retained", [][]driver.Value{})

	// A nonpromoted producer and an old active transaction still carry their
	// original epoch. Retained materialized output does not waive these fences.
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE live CURSOR WITH HOLD FOR SELECT 8")
	live := registry.Entries["live"]
	if core.CursorPromoted(live) {
		t.Fatal("live producer promoted before commit")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH ALL FROM live")
	if !errors.Is(err, ids.ErrCatalogReplaced) || result.Rows != nil {
		t.Fatal("live producer crossed replacement", result, err)
	}
	result, err = cursorUnitExecute(t, s, context.Background(), "COMMIT")
	if !errors.Is(err, ids.ErrCatalogReplaced) || result.Rows != nil {
		t.Fatal("old transaction committed across replacement", result, err)
	}
	sessionSQL(t, s, "ROLLBACK")
}
