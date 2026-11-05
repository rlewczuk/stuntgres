package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func bufferNUMAUnitCursor(t *testing.T, e *core.Engine, s *session.Session) expr.SourceCursor {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	e.Mu.Lock()
	core.ScopeAttachTransactionLocked(scope)
	e.Mu.Unlock()
	core.SetScopeWorked(scope, true)
	view := e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "pg_buffercache_numa").Address)
	shape, ok := view.LookupType(relation.RowType)
	if !ok { t.Fatal("NUMA ordinary view lost resident rowtype") }
	cursor, err := scope.WriteHost().Contrib().PgBuffercacheNUMA(shape)
	if err != nil { t.Fatal(err) }
	return cursor
}

func bufferNUMARow(t *testing.T, value sqltype.Value, id int64) {
	t.Helper()
	if value.IsNull() || value.Type().CompositeLen() != 3 { t.Fatal("NUMA row shape", value) }
	for i, want := range [...]int64{id, id-1, 0} {
		field := value.CompositeField(i)
		got, err := field.Integer()
		if err != nil || field.IsNull() || got != want { t.Fatal("NUMA independently specified scalar", id, i, got, err) }
	}
}

func TestPgBuffercacheNUMAOwnership(t *testing.T) {
	t.Run("registered_scalar_cursor_lifetime", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE numa_owner_heap(k integer)", "INSERT INTO numa_owner_heap VALUES(1),(2)"} { enginetest.Run(t, e, q) }
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		node := bufferUnitNode(t, e, "numa_owner_heap")
		cold := bufferNUMAUnitCursor(t, e, observer)
		defer cold.Close()
		if cold.Stop <= 0 || cold.State != nil || cold.Values != nil || cold.Release != nil || cold.Position != 0 { t.Fatal("NUMA cursor retains more than detached scalar topology", cold) }
		bufferUnitFact(t, e, observer, node, 0, 0)
		_, source, command := bufferUnitPortal(t, e, owner, "numa_heap_owner", "SELECT k FROM numa_owner_heap")
		bufferUnitFact(t, e, observer, node, 1, 1)
		before := bufferUnitFacts(t, e, observer)
		cursor := bufferNUMAUnitCursor(t, e, observer)
		defer cursor.Close()
		if cursor.Stop != cold.Stop || cursor.State != nil || cursor.Values != nil || cursor.Release != nil { t.Fatal("warm occupancy leaked into NUMA cursor", cursor) }
		var retained sqltype.Value
		for id := int64(1); id <= cursor.Stop; id++ {
			value, present, err := cursor.Next(t.Context())
			if err != nil || !present { t.Fatal("NUMA capacity gap", id, present, err) }
			if id == 1 || id == cursor.Stop/2 || id == cursor.Stop { bufferNUMARow(t, value, id) }
			if id == 1 { retained = value }
			if cursor.State != nil || cursor.Values != nil || cursor.Release != nil { t.Fatal("iteration retained observation resources", cursor) }
		}
		if _, present, err := cursor.Next(t.Context()); err != nil || present || !cursor.Exhausted { t.Fatal("NUMA exhaustion", present, err, cursor) }
		bufferNUMARow(t, retained, 1)
		if after := bufferUnitFacts(t, e, observer); !reflect.DeepEqual(before, after) { t.Fatal("NUMA iteration changed active source occupancy/usage/pins", before, after) }
		canceled := bufferNUMAUnitCursor(t, e, observer)
		if _, present, err := canceled.Next(t.Context()); err != nil || !present { t.Fatal("midcursor setup", present, err) }
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, present, err := canceled.Next(ctx); !errors.Is(err, context.Canceled) || present || !canceled.Exhausted || canceled.State != nil || canceled.Values != nil { t.Fatal("midcursor cancellation", present, err, canceled) }
		if after := bufferUnitFacts(t, e, observer); !reflect.DeepEqual(before, after) { t.Fatal("NUMA cancellation changed active owner pins", before, after) }
		early := bufferNUMAUnitCursor(t, e, observer)
		if _, present, err := early.Next(t.Context()); err != nil || !present { t.Fatal("early-close setup", present, err) }
		early.Close()
		if _, present, err := early.Next(t.Context()); err != nil || present || !early.Exhausted { t.Fatal("closed scalar cursor borrowed authority", present, err) }
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("numa_heap_owner") })
		bufferUnitReleased(t, source)
		enginetest.SessionSQL(t, owner, "ROLLBACK")
		bufferUnitFact(t, e, observer, node, 1, 0)
		beforeReplacement := e.CatalogRead()
		if _, err := e.ReplaceCatalog(t.Context(), e, beforeReplacement); err != nil { t.Fatal(err) }
		newNode := bufferUnitNode(t, e, "numa_owner_heap")
		bufferUnitFact(t, e, observer, newNode, 0, 0)
		value, present, err := cold.Next(t.Context())
		if err != nil || !present { t.Fatal("detached old scalar cursor after replacement", present, err) }
		bufferNUMARow(t, value, 1)
		bufferNUMARow(t, retained, 1)
		fresh := bufferNUMAUnitCursor(t, e, observer)
		defer fresh.Close()
		if fresh.Stop != cold.Stop { t.Fatal("replacement changed capacity model", fresh.Stop, cold.Stop) }
		value, present, err = fresh.Next(t.Context())
		if err != nil || !present { t.Fatal("fresh registered observation", present, err) }
		bufferNUMARow(t, value, 1)
		bufferUnitFact(t, e, observer, newNode, 0, 0)
	})
	t.Run("first_demand_and_borrowed_authority", func(t *testing.T) {
		scope, call, evaluation := incomingVPCSource(t, "SELECT bufferid FROM pg_buffercache_numa_pages() AS p(bufferid integer,os_page_num bigint,numa_node integer)")
		cursor, err := call.(expr.SourceOpening).OpenSource(evaluation)
		if err != nil { t.Fatal(err) }
		defer cursor.Close()
		if cursor.Position != -1 || cursor.Stop != 0 || cursor.State != nil || cursor.Values != nil { t.Fatal("metadata open captured topology", cursor) }
		advance := call.(expr.SourceAdvancing)
		value, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor)
		if err != nil || !present || cursor.Position != 1 || cursor.Stop <= 0 || cursor.State != nil || cursor.Values != nil || cursor.Release != nil { t.Fatal("first demand did not capture scalar capacity", present, err, cursor) }
		bufferNUMARow(t, value, 1)
		lost := *evaluation
		lost.RoutineInitialization = exec.RoutineInitialization{}
		if _, present, err := advance.AdvanceSource(t.Context(), &lost, &cursor); err == nil || present || !cursor.Exhausted || cursor.State != nil {
			t.Fatal("NUMA cursor retained borrowed initialization authority", present, err, cursor)
		}
		cursor, err = call.(expr.SourceOpening).OpenSource(evaluation)
		if err != nil { t.Fatal(err) }
		if _, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor); err != nil || !present { t.Fatal("fresh demand after owner rejection", present, err) }
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, present, err := advance.AdvanceSource(ctx, nil, &cursor); !errors.Is(err, context.Canceled) || present || !cursor.Exhausted { t.Fatal("borrowed midcursor cancellation", present, err) }
		if err := scope.Finish(nil); err != nil { t.Fatal(err) }
		if _, present, err := advance.AdvanceSource(t.Context(), nil, &cursor); err != nil || present { t.Fatal("detached canceled cursor borrowed finished owner", present, err) }
	})
}
