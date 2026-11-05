package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func evictionUnitCarrier(t *testing.T, e *core.Engine, kind functions.PgBuffercacheEvictionKind) plan.InstalledPgBuffercacheEvictionExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	name := "pg_buffercache_evict_all"
	var args []parser.Expr
	if kind == functions.EvictBuffer {
		name = "pg_buffercache_evict"
		args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}
	} else if kind == functions.EvictRelation {
		name = "pg_buffercache_evict_relation"
		args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}}
	}
	node, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
	if err != nil { t.Fatal(err) }
	return node.(plan.InstalledPgBuffercacheEvictionExpr)
}
func evictionUnitAction(t *testing.T, e *core.Engine, observer *session.Session, kind functions.PgBuffercacheEvictionKind, input int64) [3]int64 {
	t.Helper()
	n := evictionUnitCarrier(t, e, kind)
	op, err := observer.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	// Enter a genuine command snapshot before using its authenticated host,
	// as the existing direct contrib owner fixtures do. Transaction attachment
	// alone does not establish statement publication authority.
	setup, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if setup.Rows != nil { setup.Rows.Close() }
	if err != nil { t.Fatal(err) }
	result, err := scope.WriteHost().Contrib().PgBuffercacheEviction(n, input)
	if err != nil { t.Fatal(err) }
	return result
}
func evictionUnitRelation(t *testing.T, e *core.Engine, name string) int64 {
	t.Helper()
	return int64(enginetest.CatalogRef(t, e, e.CatalogRead(), name).Address.ObjectID)
}
func evictionUnitResult(t *testing.T, actual [3]int64, evicted, skipped int64) {
	t.Helper()
	if actual != [3]int64{evicted, 0, skipped} { t.Fatal("performed owner mutation", actual, evicted, skipped) }
}

func TestPgBuffercacheEvictionSourceOwnership(t *testing.T) {
	for _, terminal := range []string{"early_close", "execution_error", "cancellation", "session_end"} {
		t.Run(terminal, func(t *testing.T) {
			e := bufferUnitEngine(t)
			enginetest.Run(t, e, "CREATE TABLE eviction_owned(k integer)")
			enginetest.Run(t, e, "INSERT INTO eviction_owned VALUES(1),(2)")
			owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			portal, source, command := bufferUnitPortal(t, e, owner, "eviction_owned", "SELECT k FROM eviction_owned")
			lease := source.ReadLease
			if lease == nil { t.Fatal("real suspended source omitted lease") }
			node := bufferUnitNode(t, e, "eviction_owned")
			oid := evictionUnitRelation(t, e, "eviction_owned")
			bufferUnitFact(t, e, observer, node, 1, 1)
			detached := bufferUnitCursor(t, e, observer)
			defer detached.Close()
			evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictRelation, oid), 0, 1)
			evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 0, 1)
			bufferUnitFact(t, e, observer, node, 1, 1)
			switch terminal {
			case "early_close":
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("eviction_owned") })
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			case "execution_error":
				sentinel := errors.New("eviction retained consumer failed")
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("eviction_owned", 0, func([]session.ProtocolField) error { return sentinel }, nil, nil, core.CopyProtocolHooks{})
					if !errors.Is(err, sentinel) { t.Fatal("consumer failure boundary", err) }
					return nil
				})
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			case "cancellation":
				ctx, cancel := context.WithCancel(t.Context())
				op, _, err := owner.AcquireCommand(ctx)
				if err != nil { cancel(); t.Fatal(err) }
				scope, err := e.StartProtocolCommand(op, owner.Transactions.ProtocolCycle)
				if err != nil { op.Done(); cancel(); t.Fatal(err) }
				_, err = scope.ExecuteProtocolPortal("eviction_owned", 0, func([]session.ProtocolField) error { cancel(); return context.Canceled }, nil, nil, core.CopyProtocolHooks{})
				finished := scope.Finish(err)
				op.Done()
				cancel()
				if !errors.Is(err, context.Canceled) || !errors.Is(finished, context.Canceled) { t.Fatal("actual cancel boundary", err, finished) }
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			case "session_end":
				owner.RequestEnd(session.CleanClose)
				ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
				err := owner.WaitEnd(ctx)
				cancel()
				if err != nil { t.Fatal(err) }
			}
			bufferUnitReleased(t, source)
			if core.PortalProducer(portal) != nil { t.Fatal("terminal portal retained producer") }
			bufferUnitFact(t, e, observer, node, 1, 0)
			evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictRelation, oid), 1, 0)
			bufferUnitFact(t, e, observer, node, 0, 0)
			found := false
			for {
				row, present, err := detached.Next(t.Context())
				if err != nil { t.Fatal(err) }
				if !present { break }
				if row.CompositeField(1).IsNull() { continue }
				retainedNode, err := row.CompositeField(1).OID()
				if err != nil { t.Fatal(err) }
				if retainedNode != uint32(node) { continue }
				pins, err := row.CompositeField(8).Integer()
				if err != nil || pins != 1 { t.Fatal("eviction mutated detached genuine owner snapshot", pins, err) }
				found = true
				break
			}
			if !found { t.Fatal("eviction erased detached genuine storage identity") }
			enginetest.Run(t, e, "SELECT k FROM eviction_owned")
			lease.Close() // A real old lease cannot release a replacement.
			bufferUnitFact(t, e, observer, node, 1, 0)
			evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 1, 0)
		})
	}
	t.Run("control_generation_fences_old_lease", func(t *testing.T) {
		e := bufferUnitEngine(t)
		enginetest.Run(t, e, "CREATE TABLE eviction_generation(k integer)")
		enginetest.Run(t, e, "INSERT INTO eviction_generation VALUES(1),(2)")
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		_, old, command := bufferUnitPortal(t, e, owner, "eviction_old", "SELECT k FROM eviction_generation")
		lease := old.ReadLease
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("eviction_old") })
		enginetest.SessionSQL(t, owner, "ROLLBACK")
		before := e.CatalogRead()
		if _, err := e.ReplaceCatalog(t.Context(), e, before); err != nil { t.Fatal(err) }
		node := bufferUnitNode(t, e, "eviction_generation")
		bufferUnitFact(t, e, observer, node, 0, 0)
		_, fresh, closeFresh := bufferUnitPortal(t, e, owner, "eviction_fresh", "SELECT k FROM eviction_generation")
		lease.Open()
		lease.Close()
		bufferUnitFact(t, e, observer, node, 1, 1)
		evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 0, 1)
		closeFresh(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("eviction_fresh") })
		bufferUnitReleased(t, fresh)
		enginetest.SessionSQL(t, owner, "ROLLBACK")
		evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 1, 0)
	})
}

func TestPgBuffercacheEvictionCrossDatabase(t *testing.T) {
	e := engine.NewWithSettings(nil, "eviction_source", false)
	enginetest.Run(t, e, "CREATE EXTENSION pg_buffercache")
	enginetest.Run(t, e, "CREATE TABLE eviction_peer(k integer)")
	enginetest.Run(t, e, "INSERT INTO eviction_peer VALUES(1)")
	enginetest.Run(t, e, "CREATE DATABASE eviction_other")
	other, err := e.SelectDatabase(t.Context(), "eviction_other")
	if err != nil { t.Fatal(err) }
	for _, q := range []string{"CREATE EXTENSION pg_buffercache", "CREATE TABLE eviction_peer(k integer)", "INSERT INTO eviction_peer VALUES(1)", "SELECT k FROM eviction_peer"} { enginetest.Run(t, other, q) }
	enginetest.Run(t, e, "SELECT k FROM eviction_peer")
	observer := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, other, nil)
	left, right := bufferUnitNode(t, e, "eviction_peer"), bufferUnitNode(t, other, "eviction_peer")
	if e.Cluster != other.Cluster || e.Catalog.Domain == other.Catalog.Domain || e.Catalog.DatabaseAddress() == other.Catalog.DatabaseAddress() { t.Fatal("not distinct genuine database owners") }
	bufferUnitDatabaseFact(t, e, observer, other.Catalog.DatabaseAddress().ObjectID, right, 1)
	evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictRelation, evictionUnitRelation(t, e, "eviction_peer")), 1, 0)
	bufferUnitDatabaseFact(t, other, peer, e.Catalog.DatabaseAddress().ObjectID, left, 0)
	bufferUnitDatabaseFact(t, e, observer, other.Catalog.DatabaseAddress().ObjectID, right, 1)
	independent := bufferUnitEngine(t)
	enginetest.Run(t, independent, "CREATE TABLE eviction_peer(k integer)")
	enginetest.Run(t, independent, "INSERT INTO eviction_peer VALUES(1)")
	enginetest.Run(t, independent, "SELECT k FROM eviction_peer")
	isolated := enginetest.StartSession(t, independent, nil)
	evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 1, 0)
	bufferUnitDatabaseFact(t, other, peer, other.Catalog.DatabaseAddress().ObjectID, right, 0)
	bufferUnitFact(t, independent, isolated, bufferUnitNode(t, independent, "eviction_peer"), 1, 0)
}
