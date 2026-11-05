package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func pgssResetOwnerDrain(t *testing.T, e *core.Engine, s *session.Session, query string, count int) sqltype.Value {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = query, true
	result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
	if err != nil || result.Rows == nil {
		t.Fatal("missing real reset owner producer", err)
	}
	defer result.Rows.Close()
	var row [1]sqltype.Value
	var first sqltype.Value
	rows := 0
	for {
		more, err := result.Rows.Next(context.Background(), row[:])
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		if rows == 0 {
			first = row[0]
		}
		rows++
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if rows != count {
		t.Fatal("actual owner processed rows", rows, count)
	}
	return first
}

// Actual protocol definitions, accepted graphs and terminal resources belong to
// core. Public SQL outcomes independently live in reference/pgstatstatements.
func TestPgStatStatementsResetFrame(t *testing.T) {
	t.Run("accepted_resource_and_terminal", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE EXTENSION pg_stat_statements", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
			pgssOwnerExec(t, e, s, q)
		}
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
		const query = "SELECT pg_stat_statements_reset()"
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("reset_definition", query, nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("reset_definition")
		if definition == nil || definition.Description.Folds == nil || definition.Description.Analysis == nil {
			t.Fatal("missing real reset definition owners")
		}
		owner, analysis := definition.Description.Folds, definition.Description.Analysis
		manifest := slices.Clone(analysis.Dependencies)
		bind := func(name string) *core.PreparedBinding {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "reset_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			binding := registry.Binding(name)
			if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil || binding.Entry.Description.Folds != owner || binding.Entry.Description.Analysis != analysis {
				t.Fatal("reset Bind lost genuine definition/accepted graph/group")
			}
			return binding
		}
		first := bind("reset_first")
		root, accepted := first.Resource.Root, *first.Folds
		if len(root.Expressions) != 1 {
			t.Fatal("missing accepted reset expression", root.Expressions)
		}
		call, ok := root.Expressions[0].(plan.InstalledPgStatStatementsResetExpr)
		if !ok || call.Procedure == nil || call.RoutineReference != call.Procedure || call.Procedure.Origin.Declaration != 2 {
			t.Fatal("accepted graph lost installed scalar identity", root.Expressions[0])
		}
		pgssPhaseCounts(t, e, "pg_stat_statements_reset", 1, 0, 0)
		execute := func(name string, sink func([]session.ProtocolField) error) error {
			return enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
				_, err := scope.ExecuteProtocolPortal(name, 0, sink, nil, nil, core.CopyProtocolHooks{})
				return err
			})
		}
		rows := 0
		if err := execute("reset_first", func(fields []session.ProtocolField) error {
			rows++
			if len(fields) != 1 || fields[0].Null || len(fields[0].Bytes) == 0 {
				t.Fatal("reset failed to return actual timestamp", fields)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || len(e.Cluster.StatementStatistics.Snapshot()) != 0 || first.Resource.Root != root || first.Folds.Generation != accepted.Generation {
			t.Fatal("Execute eagerly credited reset or changed accepted artifacts")
		}
		if err := execute("reset_first", func([]session.ProtocolField) error { t.Fatal("exhausted reset evaluated again"); return nil }); err != nil {
			t.Fatal(err)
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("reset_first") })
		pgssPhaseCounts(t, e, "pg_stat_statements_reset", 0, 1, 1)
		if !first.Retired || first.Resource.Root != nil || first.Folds != nil || !reflect.DeepEqual(root.Expressions[0], call) || !reflect.DeepEqual(analysis.Dependencies, manifest) || definition.Description.Folds != owner {
			t.Fatal("terminal release leaked resources or mutated immutable recipe/manifest")
		}
		second := bind("reset_second")
		if second.Resource.Root == root || second.Resource == first.Resource {
			t.Fatal("retained definition reused another execution's private graph/resource")
		}
		failure := errors.New("reset output consumer failure")
		if err := execute("reset_second", func([]session.ProtocolField) error { return failure }); !errors.Is(err, failure) {
			t.Fatal("real failed producer did not preserve consumer error", err)
		}
		if len(e.Cluster.StatementStatistics.Snapshot()) != 0 || second.Resource.Root != nil || second.Folds != nil {
			t.Fatal("failed reset caller credited success or retained live producer")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("reset_second") })
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("reset_definition") })
		if owner.Live() || len(e.Cluster.StatementStatistics.Snapshot()) != 0 || !reflect.DeepEqual(root.Expressions[0], call) {
			t.Fatal("definition/transaction cleanup duplicated terminal success or mutated saved recipe")
		}
	})
	for _, nativeFence := range []bool{false, true} {
		t.Run(map[bool]string{false: "sql_late_completion", true: "native_fence_not_revived"}[nativeFence], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE EXTENSION pg_stat_statements", "CREATE TABLE reset_frame_data(k integer)", "INSERT INTO reset_frame_data VALUES(1),(2)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t, e, s, q)
			}
			pgssOwnerExec(t, e, peer, "SET pg_stat_statements.track='none'")
			pgssResetOwnerDrain(t, e, s, "SELECT k AS reset_frame_sample FROM reset_frame_data", 2)
			pgssPhaseCounts(t, e, "reset_frame_sample", 0, 1, 2)
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("old_frame", "SELECT k AS reset_frame_sample FROM reset_frame_data", nil)
				return err
			})
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal("old_portal", "old_frame", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			binding := core.PayloadOf(s).PreparedSQL.Binding("old_portal")
			resource := binding.Resource
			command(func(scope *core.StatementScope) error {
				result, err := scope.ExecuteProtocolPortal("old_portal", 1, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
				if err == nil && !result.Suspended {
					t.Fatal("actual old executor did not suspend")
				}
				return err
			})
			if nativeFence {
				e.Cluster.StatementStatistics.Clear(statistics.StatementFilter{}, false, time.Unix(100, 0))
			}
			reset := pgssResetOwnerDrain(t, e, peer, "SELECT pg_stat_statements_reset()", 1)
			at, err := reset.TimestampTime()
			if err != nil || !e.Cluster.StatementStatistics.Info().StatsReset.Equal(at) {
				t.Fatal("actual peer SQL body did not reset global timestamp", at, err)
			}
			if len(e.Cluster.StatementStatistics.Snapshot()) != 0 {
				t.Fatal("reset recreated active executor before genuine terminal")
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("old_portal") })
			if binding.Resource != resource || resource.Root != nil || binding.Folds != nil || !binding.Retired {
				t.Fatal("old terminal failed once-only resource release")
			}
			if nativeFence {
				if len(e.Cluster.StatementStatistics.Snapshot()) != 0 {
					t.Fatal("SQL Reset revived a native replacement/Clear-fenced frame")
				}
			} else {
				pgssPhaseCounts(t, e, "reset_frame_sample", 0, 1, 1)
			}
		})
	}
	t.Run("nested_actual_frames", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE EXTENSION pg_stat_statements", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "CREATE FUNCTION reset_frame_nested() RETURNS timestamptz LANGUAGE SQL VOLATILE SET search_path=public AS $$SELECT pg_stat_statements_reset()$$", "SET pg_stat_statements.track='all'"} {
			pgssOwnerExec(t, e, s, q)
		}
		pgssResetOwnerDrain(t, e, s, "SELECT reset_frame_nested()", 1)
		snapshot := e.Cluster.StatementStatistics.Snapshot()
		if len(snapshot) != 2 {
			t.Fatal("nested reset did not complete actual child/parent frames", snapshot)
		}
		var top, nested int
		for _, entry := range snapshot {
			if entry.Planning.Count != 0 || entry.Execution.Count != 1 || entry.Rows != 1 {
				t.Fatal("nested reset manufactured/replayed phase success", entry)
			}
			if entry.Key.TopLevel {
				top++
			} else {
				nested++
			}
		}
		if top != 1 || nested != 1 {
			t.Fatal("nested reset lost terminal nesting provenance", snapshot)
		}
	})
	t.Run("canceled_before_body", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE EXTENSION pg_stat_statements", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track='top'"} {
			pgssOwnerExec(t, e, s, q)
		}
		pgssResetOwnerDrain(t, e, s, "SELECT 7 AS reset_canceled_seed", 1)
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("canceled_reset", "SELECT pg_stat_statements_reset()", nil)
			return err
		})
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal("canceled_reset_portal", "canceled_reset", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := core.PayloadOf(s).PreparedSQL.Binding("canceled_reset_portal")
		root := binding.Resource.Root
		snapshot := func() map[statistics.StatementKey]statistics.StatementEntry {
			entries := make(map[statistics.StatementKey]statistics.StatementEntry)
			for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
				entries[entry.Key] = entry
			}
			return entries
		}
		before, info := snapshot(), e.Cluster.StatementStatistics.Info()
		err := enginetest.PreparedFoldProtocolTry(t, e, s, cycle, true, func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal("canceled_reset_portal", 0, func([]session.ProtocolField) error { t.Fatal("canceled reset published row"); return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(snapshot(), before) || e.Cluster.StatementStatistics.Info() != info {
			t.Fatal("pre-body cancellation replaced error or mutated collector", err)
		}
		if root == nil || binding.Resource.Root != nil || binding.Folds != nil {
			t.Fatal("canceled caller leaked accepted live resources")
		}
	})
}
