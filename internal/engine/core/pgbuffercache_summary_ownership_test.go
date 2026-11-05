package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func summaryUnitCounts(t *testing.T, e *core.Engine, observer *session.Session) functions.PgBuffercacheCounts {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	node, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache_summary"}}}})
	if err != nil { t.Fatal(err) }
	n := node.(plan.InstalledPgBuffercacheAggregateExpr)
	op, err := observer.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	setup, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if setup.Rows != nil { setup.Rows.Close() }
	if err != nil { t.Fatal(err) }
	counts, err := scope.WriteHost().Contrib().PgBuffercacheAggregate(n)
	if err != nil { t.Fatal(err) }
	return counts
}
func summaryUnitPins(t *testing.T, e *core.Engine, observer *session.Session, used, pinned int64) functions.PgBuffercacheCounts {
	t.Helper()
	counts := summaryUnitCounts(t, e, observer)
	var buffers, pins int64
	for _, b := range counts.Buckets { buffers += b.Buffers; pins += b.Pinned }
	if counts.Used != used || counts.Pinned != pinned || pins != pinned || buffers != counts.Capacity || counts.Dirty != 0 { t.Fatal("real shared buffer owner aggregation", counts, used, pinned) }
	return counts
}

func TestPgBuffercacheSummarySourceOwnership(t *testing.T) {
	t.Run("aliases_sessions_distinct_buffers", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE summary_pins(k integer)", "INSERT INTO summary_pins VALUES(1),(2)", "CREATE TABLE summary_other(k integer)", "INSERT INTO summary_other VALUES(1),(2)"} { enginetest.Run(t, e, q) }
		a, b, c, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		_, left, closeA := bufferUnitPortal(t, e, a, "summary_a", "SELECT a.k FROM summary_pins a CROSS JOIN summary_pins b")
		if len(left.Sources) != 2 || left.Sources[0].ReadLease == nil || left.Sources[1].ReadLease == nil || left.Sources[0].ReadLease == left.Sources[1].ReadLease { t.Fatal("missing genuine distinct alias captures") }
		aliases := left.Sources
		summaryUnitPins(t, e, observer, 1, 1)
		_, right, closeB := bufferUnitPortal(t, e, b, "summary_b", "SELECT k FROM summary_pins")
		node := bufferUnitNode(t, e, "summary_pins")
		bufferUnitFact(t, e, observer, node, 3, 2)
		detached := summaryUnitPins(t, e, observer, 1, 1)
		_, other, closeC := bufferUnitPortal(t, e, c, "summary_c", "SELECT k FROM summary_other")
		summaryUnitPins(t, e, observer, 2, 2)
		closeA(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("summary_a") })
		bufferUnitReleased(t, left)
		for _, alias := range aliases { bufferUnitReleased(t, alias) }
		bufferUnitFact(t, e, observer, node, 3, 1)
		summaryUnitPins(t, e, observer, 2, 2)
		closeC(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("summary_c") })
		bufferUnitReleased(t, other)
		summaryUnitPins(t, e, observer, 2, 1)
		closeB(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("summary_b") })
		bufferUnitReleased(t, right)
		summaryUnitPins(t, e, observer, 2, 0)
		if detached.Used != 1 || detached.Pinned != 1 || detached.Buckets[3].Pinned != 1 { t.Fatal("detached aggregate borrowed live map/owners", detached) }
		for _, s := range []*session.Session{a,b,c} { enginetest.SessionSQL(t, s, "ROLLBACK") }
	})
	for _, terminal := range []string{"early_close", "execution_error", "cancellation", "session_end"} {
		t.Run(terminal, func(t *testing.T) {
			e := bufferUnitEngine(t)
			enginetest.Run(t, e, "CREATE TABLE summary_terminal(k integer)")
			enginetest.Run(t, e, "INSERT INTO summary_terminal VALUES(1),(2)")
			owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			portal, source, command := bufferUnitPortal(t, e, owner, "summary_terminal", "SELECT k FROM summary_terminal")
			lease := source.ReadLease
			if lease == nil { t.Fatal("suspended real heap has no owner lease") }
			detached := summaryUnitPins(t, e, observer, 1, 1)
			switch terminal {
			case "early_close":
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("summary_terminal") })
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			case "execution_error":
				sentinel := errors.New("summary retained consumer failed")
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("summary_terminal", 0, func([]session.ProtocolField) error { return sentinel }, nil, nil, core.CopyProtocolHooks{})
					if !errors.Is(err, sentinel) { t.Fatal("consumer error replaced", err) }
					return nil
				})
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			case "cancellation":
				ctx, cancel := context.WithCancel(t.Context())
				op, _, err := owner.AcquireCommand(ctx)
				if err != nil { cancel(); t.Fatal(err) }
				scope, err := e.StartProtocolCommand(op, owner.Transactions.ProtocolCycle)
				if err != nil { op.Done(); cancel(); t.Fatal(err) }
				_, err = scope.ExecuteProtocolPortal("summary_terminal", 0, func([]session.ProtocolField) error { cancel(); return context.Canceled }, nil, nil, core.CopyProtocolHooks{})
				finished := scope.Finish(err)
				op.Done()
				cancel()
				if !errors.Is(err, context.Canceled) || !errors.Is(finished, context.Canceled) { t.Fatal("actual cancellation boundary", err, finished) }
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
			summaryUnitPins(t, e, observer, 1, 0)
			lease.Close()
			summaryUnitPins(t, e, observer, 1, 0)
			if detached.Pinned != 1 { t.Fatal("terminal cleanup mutated detached counts") }
		})
	}
}
