package core_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These owner tests stay beside core so its existing _test exports expose the
// actual portal binding and PL transaction rotation without production shims.
func pgssOwnerExec(t *testing.T, e *core.Engine, s *session.Session, text string) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = text, true
	result, err := e.Execute(op, enginetest.Statement(t, text), 0, nil)
	if result.Rows != nil {
		if closeErr := result.Rows.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		t.Fatal(text, err)
	}
}

func pgssPhaseEntry(t *testing.T, e *core.Engine, marker string) statistics.StatementEntry {
	t.Helper()
	var entry statistics.StatementEntry
	found := false
	for _, candidate := range e.Cluster.StatementStatistics.Snapshot() {
		if strings.Contains(candidate.Query, marker) {
			if found {
				t.Fatal("split logical phase identity", marker)
			}
			entry = candidate
			found = true
		}
	}
	return entry
}

func pgssPhaseCounts(t *testing.T, e *core.Engine, marker string, plans, calls, rows int64) {
	t.Helper()
	entry := pgssPhaseEntry(t, e, marker)
	if entry.Planning.Count != plans || entry.Execution.Count != calls || entry.Rows != rows {
		t.Fatal(marker, "phase counts", entry, plans, calls, rows)
	}
	for _, sample := range []statistics.StatementMoments{entry.Planning, entry.Execution} {
		if math.IsNaN(sample.Total) || math.IsInf(sample.Total, 0) || sample.Total < 0 || sample.Min < 0 || sample.Max < sample.Min || sample.Mean < sample.Min || sample.Mean > sample.Max || sample.Stddev() < 0 {
			t.Fatal("non-real or incoherent measurement", sample)
		}
	}
}

func TestPgStatStatementsCompletionRejectedPlanning(t *testing.T) {
	for _, failure := range []string{"canceled", "retired_attempt", "invalidated_attempt", "static_fold"} {
		t.Run(failure, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t, e, s, q)
			}
			query := "SELECT (point '1,2')::text AS phase_rejected"
			if failure == "static_fold" {
				query = "SELECT 1/0 AS phase_rejected"
			}
			ast, description := enginetest.PreparedDescription(t, e, s, query)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			portal := core.NewProtocolPortal(core.PreparedDefinition{Body: ast, Source: query, Description: description}, scope.TxRef)
			core.SetProtocolScopeBind(core.ScopeProtocol(scope), portal)
			scope.Expected = &core.PortalEntryRef(portal).Description
			owner := description.Folds
			imported := false
			_, err = scope.Execute(ast, 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
				imported = true
				switch failure {
				case "canceled":
					cancel()
				case "retired_attempt":
					owner.Retire()
				case "invalidated_attempt":
					owner.Invalidate(true)
				}
				return nil
			})
			err = scope.Finish(err)
			if !imported || err == nil {
				t.Fatal("real construction/publication rejection was not exercised", failure, err)
			}
			switch failure {
			case "canceled":
				if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
					t.Fatal("cancellation was replaced by a different fence", err)
				}
			case "retired_attempt", "invalidated_attempt":
				if !errors.Is(err, ids.ErrCatalogReplaced) || err.Error() != ids.ErrCatalogReplaced.Error() {
					t.Fatal("exact-attempt rejection diagnostic", failure, err)
				}
			case "static_fold":
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			}
			if portal.Folds != nil || portal.Resource.Root != nil {
				t.Fatal("rejected planning published a usable portal")
			}
			pgssPhaseCounts(t, e, "phase_rejected", 0, 0, 0)
		})
	}
}

func TestPgStatStatementsCompletionRoutineRebase(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
		pgssOwnerExec(t, e, s, q)
	}
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("rebase_definition", "SELECT 7 AS phase_rebase", nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("rebase_portal", "rebase_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	pgssPhaseCounts(t, e, "phase_rebase", 1, 0, 0)
	command(func(scope *core.StatementScope) error {
		if err := scope.CloseProtocolPortal("rebase_portal"); err != nil {
			return err
		}
		pgssPhaseCounts(t, e, "phase_rebase", 1, 0, 0)
		before := scope.TxRef
		restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
		err := core.ScopeRotatePLTransaction(scope, true, false)
		err = restore(err)
		if err == nil && scope.TxRef == before {
			t.Fatal("routine did not rotate the actual transaction owner")
		}
		pgssPhaseCounts(t, e, "phase_rebase", 1, 0, 0)
		return err
	})
	pgssPhaseCounts(t, e, "phase_rebase", 1, 1, 0)
}
