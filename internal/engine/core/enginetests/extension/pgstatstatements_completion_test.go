package extension_test

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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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
func pgssPreparedPhase(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, description *core.Description, source string, input int64, late error) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = source, true
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = description
	importer := func(_ context.Context, formal analyze.ParameterInfo, values []sqltype.Value, _ sqltype.InputEnvironment) error {
		var err error
		values[0], err = sqltype.Integer(formal.Types[0].Type, input)
		return err
	}
	result, err := scope.Execute(ast, description.Parameters.Count, importer)
	if err == nil && late != nil {
		err = late
	}
	err = scope.Finish(err)
	if result.Rows != nil {
		if cleanup := result.Rows.Close(); err == nil {
			err = cleanup
		}
	}
	return err
}

func TestPgStatStatementsCompletionPhases(t *testing.T) {
	for _, mode := range []string{"force_generic_plan", "force_custom_plan"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_table(k integer)", "INSERT INTO phase_table VALUES(1)", "SET plan_cache_mode='" + mode + "'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t, e, s, q)
			}
			source := "SELECT 10/(k-$1::integer) AS phase_value FROM phase_table"
			ast, description := enginetest.PreparedDescription(t, e, s, source)
			pgssPhaseCounts(t, e, "phase_value", 0, 0, 0)
			for i := int64(1); i <= 2; i++ {
				if err := pgssPreparedPhase(t, e, s, ast, &description, source, 0, nil); err != nil {
					t.Fatal(err)
				}
				pgssPhaseCounts(t, e, "phase_value", i, i, i)
			}
			// Table-dependent division is demanded by execution, after the real native
			// preflight and its exact-attempt acceptance. A retained literal is not a plan.
			err := pgssPreparedPhase(t, e, s, ast, &description, source, 1, nil)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
				t.Fatal("runtime division boundary", err)
			}
			pgssPhaseCounts(t, e, "phase_value", 3, 2, 2)
			if _, err := pgssOwnerSQL(t, e, s, "SELECT 1/0 AS phase_runtime_constant"); !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
				t.Fatal("existing row-owned arithmetic failure", err)
			}
			pgssPhaseCounts(t, e, "phase_runtime_constant", 1, 0, 0)
			late := errors.New("injected terminal rejection at Finish")
			if err := pgssPreparedPhase(t, e, s, ast, &description, source, 0, late); !errors.Is(err, late) {
				t.Fatal(err)
			}
			pgssPhaseCounts(t, e, "phase_value", 4, 2, 2)
		})
	}
}

func TestPgStatStatementsCompletionPortal(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_table(k integer)", "INSERT INTO phase_table VALUES(1),(2)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'", "BEGIN"} {
		pgssOwnerExec(t, e, s, q)
	}
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := "SELECT k,(point '1,2')::text AS phase_portal FROM phase_table ORDER BY k"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("phase_definition", query, nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("phase_definition")
		return err
	})
	pgssPhaseCounts(t, e, "phase_portal", 0, 0, 0)
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("phase_definition")
	owner := definition.Description.Folds
	if owner == nil || owner.Current().Generation != 0 {
		t.Fatal("Parse retained executable planning")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "phase_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("phase_first")
	first := registry.Binding("phase_first")
	if first == nil || first.Resource == nil || first.Resource.Root == nil || first.Folds == nil {
		t.Fatal("Bind lacks actual accepted resource")
	}
	published := owner.Current()
	if len(published.Entries) == 0 {
		t.Fatal("real typed constructor did not publish immutable backing")
	}
	bind("phase_second")
	second := registry.Binding("phase_second")
	if second == nil || second.Resource == nil || second.Resource.Root == first.Resource.Root || definition.Description.Folds != owner || owner.Current().Generation != published.Generation || &owner.Current().Entries[0] != &published.Entries[0] {
		t.Fatal("repeated real construction reused a live graph or mutated accepted backing")
	}
	pgssPhaseCounts(t, e, "phase_portal", 2, 0, 0)
	execute := func(name string, limit uint32) int {
		rows := 0
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, limit, func([]session.ProtocolField) error { rows++; return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		return rows
	}
	if rows := execute("phase_first", 1); rows != 1 {
		t.Fatal(rows)
	}
	pgssPhaseCounts(t, e, "phase_portal", 2, 0, 0)
	if rows := execute("phase_first", 0); rows != 1 {
		t.Fatal(rows)
	}
	pgssPhaseCounts(t, e, "phase_portal", 2, 0, 0)
	if rows := execute("phase_first", 0); rows != 0 {
		t.Fatal(rows)
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("phase_first") })
	pgssPhaseCounts(t, e, "phase_portal", 2, 1, 2)
	held := e.Cluster.StatementStatistics.Snapshot()
	if rows := execute("phase_second", 1); rows != 1 {
		t.Fatal(rows)
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("phase_second") })
	pgssPhaseCounts(t, e, "phase_portal", 2, 2, 3)
	for _, entry := range held {
		if strings.Contains(entry.Query, "phase_portal") && (entry.Execution.Count != 1 || entry.Rows != 2) {
			t.Fatal("completion mutated an owned held snapshot", entry)
		}
	}
	bind("phase_unused")
	unused := registry.Binding("phase_unused")
	if unused == nil || unused.Resource == nil || unused.Resource.Root == nil || unused.Folds == nil || unused.Resource.Root == first.Resource.Root {
		t.Fatal("unused portal has no independent initialized executor")
	}
	pgssPhaseCounts(t, e, "phase_portal", 3, 2, 3)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("phase_unused") })
	pgssPhaseCounts(t, e, "phase_portal", 3, 3, 3)
	pgssOwnerExec(t, e, s, "ROLLBACK")
	pgssPhaseCounts(t, e, "phase_portal", 3, 3, 3)
}

func TestPgStatStatementsCompletionDisabled(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
	collector := e.Cluster.StatementStatistics
	e.Cluster.StatementStatistics = nil
	defer func() { e.Cluster.StatementStatistics = collector }()
	// An absent leaf cannot be touched: disabled admission must not construct a
	// fingerprint, normalize text, start a phase, or attempt terminal publication.
	pgssOwnerExec(t, e, s, "SELECT 1 AS disabled_statement")
}

func TestPgStatStatementsCompletionPlanningBind(t *testing.T) {
	for _, tc := range []struct {
		name, query, marker string
		returning           bool
	}{
		{"insert", "INSERT INTO phase_bind VALUES(3),(4)", "INSERT INTO phase_bind", false},
		{"returning", "INSERT INTO phase_bind VALUES(3),(4) RETURNING k AS phase_bind_returning", "phase_bind_returning", true},
		{"modifying_cte", "WITH changed AS (INSERT INTO phase_bind VALUES(3),(4) RETURNING k) SELECT k AS phase_bind_cte FROM changed ORDER BY k", "phase_bind_cte", true},
		{"rule", "INSERT INTO phase_bind VALUES(3),(4)", "INSERT INTO phase_bind", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_bind(k integer)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'", "BEGIN"} {
				pgssOwnerExec(t, e, s, q)
			}
			if tc.name == "rule" {
				pgssOwnerExec(t, e, s, "CREATE TABLE phase_rule_log(k integer)")
				pgssOwnerExec(t, e, s, "CREATE RULE phase_rule AS ON INSERT TO phase_bind DO ALSO INSERT INTO phase_rule_log VALUES(NEW.k)")
			}
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("planning_definition", tc.query, nil)
				return err
			})
			pgssPhaseCounts(t, e, tc.marker, 0, 0, 0)
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "planning_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("planning_unused")
			registry := core.PayloadOf(s).PreparedSQL
			unused := registry.Binding("planning_unused")
			if unused == nil || unused.Folds == nil || unused.Resource == nil || unused.Resource.Root != nil {
				t.Fatal("planning-only Bind initialized or omitted its accepted folds")
			}
			pgssPhaseCounts(t, e, tc.marker, 1, 0, 0)
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("planning_unused") })
			pgssPhaseCounts(t, e, tc.marker, 1, 0, 0)
			bind("planning_execute")
			pgssPhaseCounts(t, e, tc.marker, 2, 0, 0)
			delivered := 0
			command(func(scope *core.StatementScope) error {
				result, err := scope.ExecuteProtocolPortal("planning_execute", 1, func([]session.ProtocolField) error { delivered++; return nil }, nil, nil, core.CopyProtocolHooks{})
				if err == nil && !tc.returning && (result.Count != 2 || result.CommandTag != "INSERT 0 2") {
					t.Fatal("actual terminal DML result", result)
				}
				return err
			})
			if tc.returning && delivered != 1 || !tc.returning && delivered != 0 {
				t.Fatal("presentation demand", delivered)
			}
			// These strategies retain folds at Bind, not a complete executable graph.
			// Execute performs a second genuine successful native construction; eager
			// command rows remain two even when only one result tuple is presented.
			pgssPhaseCounts(t, e, tc.marker, 3, 0, 0)
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("planning_execute") })
			pgssPhaseCounts(t, e, tc.marker, 3, 1, 2)
			pgssOwnerExec(t, e, s, "COMMIT")
			pgssPhaseCounts(t, e, tc.marker, 3, 1, 2)
		})
	}
}

func TestPgStatStatementsCompletionPreCanceled(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
		pgssOwnerExec(t, e, s, q)
	}
	query := "SELECT (point '1,2')::text AS phase_precanceled"
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
	scope.Expected = &description
	cancel()
	imported := false
	_, err = scope.Execute(ast, 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		imported = true
		return nil
	})
	err = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || imported {
		t.Fatal("pre-canceled command crossed admission", err, imported)
	}
	if current := description.Folds.Current(); current.Generation != 0 || len(current.Entries) != 0 {
		t.Fatal("pre-canceled command published retained folds", current)
	}
	pgssPhaseCounts(t, e, "phase_precanceled", 0, 0, 0)
}

func TestPgStatStatementsCompletionPlainCTEPortal(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_cte_table(k integer)", "INSERT INTO phase_cte_table VALUES(1),(2)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'", "BEGIN"} {
		pgssOwnerExec(t, e, s, q)
	}
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("plain_cte_definition", "WITH phase_input AS MATERIALIZED (SELECT k FROM phase_cte_table) SELECT k AS phase_plain_cte FROM phase_input ORDER BY k", nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("plain_cte_portal", "plain_cte_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := core.PayloadOf(s).PreparedSQL.Binding("plain_cte_portal")
	if binding == nil || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil {
		t.Fatal("plain CTE Bind lacks its actual accepted producer")
	}
	pgssPhaseCounts(t, e, "phase_plain_cte", 1, 0, 0)
	delivered := 0
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("plain_cte_portal", 1, func([]session.ProtocolField) error { delivered++; return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if delivered != 1 {
		t.Fatal("partial plain CTE demand", delivered)
	}
	pgssPhaseCounts(t, e, "phase_plain_cte", 1, 0, 0)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("plain_cte_portal") })
	pgssPhaseCounts(t, e, "phase_plain_cte", 1, 1, 1)
	pgssOwnerExec(t, e, s, "COMMIT")
	pgssPhaseCounts(t, e, "phase_plain_cte", 1, 1, 1)
}
