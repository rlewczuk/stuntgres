package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func autoExplainComposedConfigure(t *testing.T, s *session.Session) {
	t.Helper()
	for _, q := range []string{"LOAD 'auto_explain'", "SET auto_explain.log_min_duration=0", "SET auto_explain.log_level=notice", "SET auto_explain.log_format=json", "SET auto_explain.log_analyze=on", "SET auto_explain.log_timing=off", "SET auto_explain.log_buffers=on", "SET auto_explain.log_wal=on"} {
		enginetest.SessionSQL(t, s, q)
	}
}

func TestAutoExplainPreparedOwnership(t *testing.T) {
	e, s, provider := explainExecutionFixture(t)
	provider.declineExecution = true
	autoExplainComposedConfigure(t, s)
	const query = "SELECT k+COALESCE(-(7::integer),0) AS v FROM executed_explain_data WHERE k=$1"
	ast, description := enginetest.PreparedDescription(t, e, s, query)
	defer e.RetirePreparedDescription(&description)
	analysis, folds, generation := description.Analysis, description.Folds, description.PlanGeneration
	backing, parameter := &description.Results[0], &description.Parameters.Types[0]
	for _, argument := range []int64{1, 2, 99, 1} {
		op := enginetest.TransactionOperation(t, s)
		op.OriginalSQL, op.SQLAvailable = query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		scope.Expected = &description
		provider.check = func() error {
			if !e.Mu.TryLock() {
				return errors.New("provider explanation holds engine mutex")
			}
			e.Mu.Unlock()
			if !e.ReplacementMu.TryLock() {
				return errors.New("provider explanation holds replacement mutex")
			}
			e.ReplacementMu.Unlock()
			return nil
		}
		imports, before := 0, len(provider.facts)
		result, err := scope.Execute(ast, 1, func(_ context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
			imports++
			var err error
			frame[0], err = sqltype.Integer(parameters.Types[0].Type, argument)
			return err
		})
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, nodes, retired := core.AutoExplainWitnessForTest(scope)
		if observation == nil || len(nodes) == 0 || imports != 1 {
			t.Fatal("accepted execution missing current observer/import")
		}
		for _, node := range nodes {
			if node != nil && !observation.Known(node) {
				t.Fatal("presentation rebound a different graph", node)
			}
		}
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if !retired() || core.AutoExplainActiveForTest(scope) || len(op.Notices) != 1 || !strings.Contains(op.Notices[0].Message, fmt.Sprintf("$1 = '%d'", argument)) {
			t.Fatal("frame/parameter lifetime", op.Notices)
		}
		for token, facts := range provider.facts {
			if token.number <= before {
				continue
			}
			if facts.begun && (!facts.explained || facts.base.Parameters.Values[0] != driver.Value(argument)) {
				t.Fatal("selected callback/startup current identity", facts)
			}
		}
		if provider.begins != provider.ends {
			t.Fatal("provider release imbalance")
		}
		if description.Analysis != analysis || description.Folds != folds || description.PlanGeneration != generation || &description.Results[0] != backing || &description.Parameters.Types[0] != parameter {
			t.Fatal("observer mutated retained definition ownership")
		}
		enginetest.LockEmpty(t, e)
	}
	// Nonzero formals deliberately do not publish into the definition cell.
	// Exercise the actual Bind-owned accepted groups as well as this direct
	// importer path, retaining their real graph/generation/backing.
	provider.check = nil
	autoExplainParameterizedFoldOwnership(t, e, s, query)
	t.Run("sample_zero_nil_path", func(t *testing.T) {
		enginetest.SessionSQL(t, s, "SET auto_explain.sample_rate=0")
		provider.ordinaryControl, provider.check = true, nil
		op := enginetest.TransactionOperation(t, s)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		result, err := scope.Execute(enginetest.Statement(t, "SELECT k FROM executed_explain_data"), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, nodes, _ := core.AutoExplainWitnessForTest(scope)
		if observation != nil || len(nodes) != 0 || core.AutoExplainActiveForTest(scope) {
			t.Fatal("sample-zero created an observer/presentation graph")
		}
		if result.Rows != nil {
			result.Rows.Close()
		}
		if err := scope.Finish(nil); err != nil || len(op.Notices) != 0 || provider.begins != provider.ends {
			t.Fatal("nil path changed ordinary execution", err, op.Notices)
		}
	})
}

func TestAutoExplainMetrics(t *testing.T) {
	t.Run("dml_roles", autoExplainDMLRoleOwnership)
	t.Run("dml_current_parameters", autoExplainDMLParameterOwnership)
	t.Run("dml_cte_invocations", autoExplainDMLCTEOwnership)
	t.Run("dml_explicit_automatic_identity", autoExplainDMLSharedOwnership)
	t.Run("dml_current_of_owner", autoExplainDMLCurrentOfOwnership)
	t.Run("dml_presentation_only", autoExplainDMLPresentationOwnership)
	t.Run("dml_nested_observer_isolation", autoExplainDMLNestedObserverOwnership)
	t.Run("joined_parallel_source_records", func(t *testing.T) {
		query, source := enginetest.QueryFixture(t, engine.New(), "SELECT id FROM t")
		pin, err := exec.ParallelQuerySource(source)
		if err != nil {
			t.Fatal(err)
		}
		ctx, observation := exec.Observe(t.Context(), false)
		observation.SetLogicalMetrics(true, false, false)
		observation.Register(query.Root)
		rows, count, err := exec.FinalizeSelect(ctx, query.Root, pin, nil, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		accesses, writes := observation.LogicalMetrics()
		if count != 4 || accesses != 4 || writes != 0 || observation.Actual(query.Root).Loops != 2 {
			t.Fatal("workers did not privately count actual partition accesses before joined read", count, accesses, writes)
		}
	})
	t.Run("fresh_base_scans", func(t *testing.T) {
		query, source := enginetest.QueryFixture(t, engine.New(), "SELECT id FROM t")
		defer source.Close()
		project, ok := query.Root.(*plan.ProjectNode)
		if !ok {
			t.Fatalf("accepted fixture root is %T, want *plan.ProjectNode", query.Root)
		}
		ctx, observation := exec.Observe(t.Context(), false)
		observation.SetLogicalMetrics(true, false, false)
		for range 2 {
			state := exec.State{Source: source, SourceMode: exec.SourceReusable}
			stage, err := exec.OpenQueryProjectStage(ctx, &state, project)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, len(query.Root.Schema()))
			for {
				present, err := stage.Next(ctx, row)
				if err != nil {
					stage.Close()
					t.Fatal(err)
				}
				if !present {
					break
				}
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			state.ReleaseMemberships()
		}
		if accesses, writes := observation.LogicalMetrics(); accesses != 8 || writes != 0 {
			t.Fatal("fresh actual scans were confused with cached replay", accesses, writes)
		}
	})
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE ae_owner(id integer,v text)")
	enginetest.SessionSQL(t, s, "INSERT INTO ae_owner VALUES(1,'one'),(2,NULL),(3,'three')")
	autoExplainComposedConfigure(t, s)
	for _, tc := range []struct {
		query            string
		accesses, writes int64
		base             bool
	}{
		{"SELECT id,v FROM ae_owner WHERE id<0", 3, 0, true},
		{"SELECT id,v FROM ae_owner LIMIT 0", 0, 0, true},
		{"SELECT id,v FROM ae_owner LIMIT 1", 1, 0, true},
		{"SELECT id,v FROM ae_owner ORDER BY id LIMIT 1", 3, 0, true},
		{"WITH a AS MATERIALIZED (SELECT id,v FROM ae_owner) SELECT id,v FROM a UNION ALL SELECT id,v FROM a", 3, 0, true},
		{"INSERT INTO ae_owner VALUES(4,'four'),(5,'five')", 0, 2, false},
		{"UPDATE ae_owner SET v='changed' WHERE id=4", 5, 1, false},
		{"DELETE FROM ae_owner WHERE id=5", 5, 1, false},
	} {
		op := enginetest.TransactionOperation(t, s)
		op.OriginalSQL, op.SQLAvailable = tc.query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		result, err := scope.Execute(enginetest.Statement(t, tc.query), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, nodes, retired := core.AutoExplainWitnessForTest(scope)
		if observation == nil {
			t.Fatal("missing actual command observer")
		}
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		accesses, writes := observation.LogicalMetrics()
		if accesses != tc.accesses || writes != tc.writes || !retired() {
			t.Fatal("actual source/staging owner metrics", tc, accesses, writes)
		}
		if tc.base {
			base := false
			for _, node := range nodes {
				if _, ok := node.(*plan.SourceNode); ok {
					base = true
					if !observation.Known(node) {
						t.Fatal("not original accepted source")
					}
				}
			}
			if !base {
				t.Fatal("no genuine base-source witness", tc.query)
			}
		}
	}
	t.Run("indexed_candidate_frontier", func(t *testing.T) {
		enginetest.SessionSQL(t, s, "CREATE INDEX ae_owner_hash ON ae_owner USING hash(id)")
		enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
		op := enginetest.TransactionOperation(t, s)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		result, err := scope.Execute(enginetest.Statement(t, "SELECT id FROM ae_owner WHERE id=2"), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, nodes, _ := core.AutoExplainWitnessForTest(scope)
		if result.Rows != nil {
			result.Rows.Close()
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		sourceRows := int64(0)
		for _, node := range nodes {
			if _, ok := node.(*plan.SourceNode); ok {
				sourceRows += observation.Actual(node).Rows
			}
		}
		if accesses, _ := observation.LogicalMetrics(); accesses != 1 || sourceRows != 1 {
			t.Fatal("indexed candidate fetch counted backing cardinality or skipped examination", accesses, sourceRows)
		}
	})
	// The same two procedure bodies are not two trigger identities. The low
	// observer supplement checks identity without pretending to execute a trigger.
	_, observation := exec.Observe(context.Background(), false)
	observation.SetLogicalMetrics(false, false, true)
	for _, oid := range []uint32{11, 12, 11, 12} {
		index, start := observation.BeginTrigger(ids.OID(oid), fmt.Sprint(oid), "owner")
		observation.EndTrigger(index, start)
	}
	if got := observation.Triggers(); len(got) != 2 || got[0].Calls != 2 || got[1].Calls != 2 || reflect.DeepEqual(got[0], got[1]) {
		t.Fatal("trigger identity collapsed", got)
	}
}

func autoExplainDMLRoleOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE ae_role(id integer PRIMARY KEY,v integer)",
		"CREATE TABLE ae_role_default(id integer DEFAULT 7)",
		"CREATE TABLE ae_role_suppressed(id integer)",
		"CREATE FUNCTION ae_role_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
		"CREATE TRIGGER ae_role_suppress BEFORE INSERT ON ae_role_suppressed FOR EACH ROW EXECUTE FUNCTION ae_role_suppress()",
		"CREATE TABLE ae_role_merge(id integer PRIMARY KEY,v integer)",
		"INSERT INTO ae_role_merge VALUES(1,10),(2,20)",
		"CREATE TABLE ae_role_empty_source(id integer,v integer)",
		"INSERT INTO ae_role_empty_source VALUES(1,10),(2,20)",
		"CREATE TABLE ae_role_empty_target(id integer,v integer)",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	autoExplainComposedConfigure(t, s)
	for _, tc := range []struct {
		query            string
		roles            map[exec.DMLRole]exec.Actual
		accesses, writes int64
	}{
		{"INSERT INTO ae_role VALUES(1,10),(2,20)", map[exec.DMLRole]exec.Actual{exec.DMLInsertInput: {Rows: 2, Loops: 1}}, 0, 2},
		{"INSERT INTO ae_role VALUES(1,99) ON CONFLICT DO NOTHING", map[exec.DMLRole]exec.Actual{exec.DMLInsertInput: {Rows: 1, Loops: 1}}, 0, 0},
		{"INSERT INTO ae_role_default DEFAULT VALUES", map[exec.DMLRole]exec.Actual{exec.DMLInsertInput: {Rows: 1, Loops: 1}}, 0, 1},
		{"INSERT INTO ae_role_suppressed VALUES(1)", map[exec.DMLRole]exec.Actual{exec.DMLInsertInput: {Rows: 1, Loops: 1}}, 0, 0},
		{"MERGE INTO ae_role_merge t USING (VALUES(1,5),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", map[exec.DMLRole]exec.Actual{exec.DMLMergeTarget: {Rows: 4, Loops: 2}, exec.DMLMergeCandidates: {Rows: 2, Loops: 1}}, 4, 2},
		{"MERGE INTO ae_role_empty_source t USING (SELECT 1 AS id WHERE false) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DO NOTHING", map[exec.DMLRole]exec.Actual{exec.DMLMergeTarget: {Rows: 2, Loops: 1}, exec.DMLMergeCandidates: {Rows: 2, Loops: 1}}, 2, 0},
		{"MERGE INTO ae_role_empty_source t USING (SELECT 1 AS id WHERE false) s ON t.id=s.id WHEN MATCHED THEN DO NOTHING", map[exec.DMLRole]exec.Actual{exec.DMLMergeTarget: {}, exec.DMLMergeCandidates: {Loops: 1}}, 0, 0},
		{"MERGE INTO ae_role_empty_target t USING (VALUES(1,10),(2,20)) s(id,v) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", map[exec.DMLRole]exec.Actual{exec.DMLMergeTarget: {Loops: 2}, exec.DMLMergeCandidates: {Rows: 2, Loops: 1}}, 0, 2},
		{"UPDATE ae_role SET v=v+1 WHERE id<0", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLPredicate: {Loops: 1}}, 2, 0},
		{"UPDATE ae_role SET v=v+1 WHERE id=1", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLPredicate: {Rows: 1, Loops: 1}}, 2, 1},
		{"DELETE FROM ae_role WHERE id=2", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLPredicate: {Rows: 1, Loops: 1}}, 2, 1},
		{"UPDATE ae_role_empty_source t SET v=v+1 FROM (SELECT 1 AS id WHERE false) s WHERE t.id=s.id", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLCandidateJoin: {Loops: 1}, exec.DMLPredicate: {Loops: 1}}, 2, 0},
		{"DELETE FROM ae_role_empty_source t USING (SELECT 1 AS id WHERE false) s WHERE t.id=s.id", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {}, exec.DMLCandidateJoin: {Loops: 1}, exec.DMLPredicate: {Loops: 1}}, 0, 0},
		{"UPDATE ae_role_empty_source t SET v=v+1 FROM (VALUES(1),(2)) s(id) WHERE t.id=s.id", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLCandidateJoin: {Rows: 4, Loops: 1}, exec.DMLPredicate: {Rows: 2, Loops: 1}}, 2, 2},
		{"DELETE FROM ae_role_empty_source t USING (VALUES(1),(2)) s(id) WHERE t.id=s.id", map[exec.DMLRole]exec.Actual{exec.DMLTargetScan: {Rows: 2, Loops: 1}, exec.DMLCandidateJoin: {Rows: 4, Loops: 1}, exec.DMLPredicate: {Rows: 2, Loops: 1}}, 2, 2},
	} {
		op := enginetest.TransactionOperation(t, s)
		op.OriginalSQL, op.SQLAvailable = tc.query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		result, err := scope.Execute(enginetest.Statement(t, tc.query), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, nodes, retired := core.AutoExplainWitnessForTest(scope)
		if observation == nil {
			t.Fatal("actual DML invocation has no observation")
		}
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		for role := exec.DMLInsertInput; role <= exec.DMLCursorLocator; role++ {
			want, exists := tc.roles[role]
			actual, known := observation.DMLActual(role)
			if exists != known || actual != want {
				t.Fatal("producer-owned role actual/reset", tc.query, role, actual, known, want, exists)
			}
		}
		originalValues := 0
		for _, node := range nodes {
			switch node.(type) {
			case *plan.SourceNode, *plan.JoinNode:
				if observation.Known(node) {
					t.Fatal("synthetic target/join acquired a fabricated OpenNode record", tc.query)
				}
			case *plan.ValuesNode, *plan.SingletonNode:
				if strings.HasPrefix(tc.query, "INSERT ") && observation.Known(node) {
					t.Fatal("literal/default adapter acquired a fabricated OpenNode record", tc.query)
				}
				if _, values := node.(*plan.ValuesNode); values && observation.Known(node) {
					originalValues++
					if actual := observation.Actual(node); actual != (exec.Actual{Rows: 2, Loops: 1}) {
						t.Fatal("genuine query source did not retain its original stage actuals", actual)
					}
				}
			}
		}
		if strings.Contains(tc.query, "USING (VALUES") && originalValues != 1 {
			t.Fatal("MERGE lost its genuine accepted source identity", tc.query, originalValues)
		}
		if accesses, writes := observation.LogicalMetrics(); accesses != tc.accesses || writes != tc.writes || !retired() {
			t.Fatal("actual DML role lifetime/access/write owners", tc.query, accesses, writes)
		}
	}
}

func autoExplainDMLParameterOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE ae_role_params(id integer,v integer)")
	autoExplainComposedConfigure(t, s)
	const query = "INSERT INTO ae_role_params VALUES($1,COALESCE(-(7::integer),0))"
	ast, description := enginetest.PreparedDescription(t, e, s, query)
	defer e.RetirePreparedDescription(&description)
	analysis, folds, generation := description.Analysis, description.Folds, description.PlanGeneration
	parameter := &description.Parameters.Types[0]
	var prior *exec.Observation
	for _, argument := range []int64{1, 2, 99, 1} {
		op := enginetest.TransactionOperation(t, s)
		op.OriginalSQL, op.SQLAvailable = query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		scope.Expected = &description
		imports := 0
		_, err = scope.Execute(ast, 1, func(_ context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
			imports++
			var err error
			frame[0], err = sqltype.Integer(parameters.Types[0].Type, argument)
			return err
		})
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		observation, _, retired := core.AutoExplainWitnessForTest(scope)
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		actual, known := observation.DMLActual(exec.DMLInsertInput)
		if !known || actual != (exec.Actual{Rows: 1, Loops: 1}) || imports != 1 || !retired() || observation == prior {
			t.Fatal("current input invocation was reused/accumulated", actual, known, imports)
		}
		if prior != nil {
			if previous, known := prior.DMLActual(exec.DMLInsertInput); !known || previous != actual {
				t.Fatal("later invocation mutated retired role measurement", previous)
			}
		}
		if len(op.Notices) != 1 || !strings.Contains(op.Notices[0].Message, fmt.Sprintf("$1 = '%d'", argument)) {
			t.Fatal("current canonical parameter not captured", op.Notices)
		}
		if description.Analysis != analysis || description.Folds != folds || description.PlanGeneration != generation || &description.Parameters.Types[0] != parameter {
			t.Fatal("DML observer changed retained definition ownership")
		}
		prior = observation
		enginetest.LockEmpty(t, e)
	}
	autoExplainParameterizedFoldOwnership(t, e, s, query)
}

func autoExplainDMLCTEOwnership(t *testing.T) {
	for _, lazy := range []bool{false, true} {
		t.Run(fmt.Sprintf("lazy=%t", lazy), func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE ae_role_cte(id integer)")
			if lazy {
				enginetest.SessionSQL(t, s, "CREATE FUNCTION ae_role_cte_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$")
				enginetest.SessionSQL(t, s, "CREATE TRIGGER ae_role_cte_before BEFORE INSERT ON ae_role_cte FOR EACH STATEMENT EXECUTE FUNCTION ae_role_cte_before()")
			}
			autoExplainComposedConfigure(t, s)
			const query = "WITH a AS (INSERT INTO ae_role_cte VALUES(1),(2) RETURNING id), b AS (INSERT INTO ae_role_cte VALUES(3) RETURNING id) SELECT id FROM a UNION ALL SELECT id FROM a UNION ALL SELECT id FROM b"
			var saved []*exec.DMLObservation
			for range 2 {
				op := enginetest.TransactionOperation(t, s)
				op.OriginalSQL, op.SQLAvailable = query, true
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				explainExecutionCleanup(t, scope)
				result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				if result.Rows != nil {
					if err := result.Rows.Close(); err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
				}
				records, _, shared := core.AutoExplainDMLRecordsForTest(scope)
				if len(records) != 2 || !shared || records[0] == records[1] {
					t.Fatal("CTE commands did not retain disjoint exact invocation records", len(records), shared)
				}
				seen := map[int64]bool{}
				for _, record := range records {
					measurement := record.Role(exec.DMLInsertInput)
					if measurement == nil {
						t.Fatal("actual CTE literal producer was not measured")
					}
					actual := measurement.Actual()
					root := record.CommandActual()
					if actual.Loops != 1 || actual.Rows < 1 || actual.Rows > 2 || seen[actual.Rows] || root.Rows != actual.Rows || root.Loops != 1 {
						t.Fatal("lazy/eager CTE demand repeated producer or mixed sibling actuals", actual, root)
					}
					seen[actual.Rows] = true
					for _, previous := range saved {
						if record == previous {
							t.Fatal("later CTE invocation reused prior measurement")
						}
					}
				}
				if len(saved) != 0 {
					for _, previous := range saved {
						actual := previous.Role(exec.DMLInsertInput).Actual()
						if actual.Loops != 1 || previous.CommandActual().Rows != actual.Rows {
							t.Fatal("sibling/later invocation overwrote retained completed CTE scalar", actual)
						}
					}
				}
				_, _, retired := core.AutoExplainWitnessForTest(scope)
				if err := scope.Finish(nil); err != nil || !retired() {
					t.Fatal("CTE frame/context did not retire", err)
				}
				saved = records
				enginetest.LockEmpty(t, e)
			}
		})
	}
}

func autoExplainDMLSharedOwnership(t *testing.T) {
	e, s, provider := explainExecutionFixture(t)
	provider.declineExecution = true
	autoExplainComposedConfigure(t, s)
	const query = "EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF) MERGE INTO executed_explain_local t USING executed_explain_data s ON t.k=s.k WHEN NOT MATCHED THEN INSERT VALUES(s.k)"
	op := enginetest.TransactionOperation(t, s)
	op.OriginalSQL, op.SQLAvailable = query, true
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	explainExecutionCleanup(t, scope)
	handoff := &autoExplainMergeHandoff{Context: op.Ctx, scope: scope}
	op.Ctx = handoff
	var saved *exec.DMLObservation
	provider.check = func() error {
		automatic, explicit, shared := core.AutoExplainDMLRecordsForTest(scope)
		if !shared || len(automatic) != 1 || len(explicit) != 1 || automatic[0] != explicit[0] {
			return errors.New("explicit and automatic adapters did not share exact command/role records")
		}
		saved = automatic[0]
		// Source Explain runs on the first cursor.Next inside membership:
		// the candidate producer has entered, but no pair/target has emitted.
		if saved != handoff.record || saved.CommandActual().Loops != 1 ||
			saved.Role(exec.DMLMergeCandidates).Actual() != (exec.Actual{Loops: 1}) ||
			saved.Role(exec.DMLMergeTarget).Actual() != (exec.Actual{}) {
			return errors.New("source startup lost genuine membership entry order/shared records")
		}
		return nil
	}
	result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if handoff.record == nil || handoff.failure != nil {
		t.Fatal("witness missed genuine pre-membership handoff", handoff.failure)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
	}
	if saved == nil || saved.CommandActual().Rows != 0 || saved.CommandActual().Loops != 1 ||
		saved.Role(exec.DMLMergeTarget).Actual() != (exec.Actual{Loops: 3}) ||
		saved.Role(exec.DMLMergeCandidates).Actual() != (exec.Actual{Rows: 3, Loops: 1}) {
		t.Fatal("shared roles did not measure the real entered-empty target passes/candidates", saved)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if provider.begins != provider.ends {
		t.Fatal("shared presentation changed selected provider lifecycle")
	}
}

func autoExplainDMLCurrentOfOwnership(t *testing.T) {
	s := cursorUnitSetup(t)
	e := core.EngineOf(s)
	enginetest.SessionSQL(t, s, "DECLARE ae_role_cursor CURSOR FOR SELECT i FROM unit_rows ORDER BY i FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM ae_role_cursor", [][]driver.Value{{int64(1)}})
	cursor := core.PayloadOf(s).Cursors.Entries["ae_role_cursor"]
	source, index, root := cursor.CurrentSource, cursor.CurrentIndex, core.CursorRoot(cursor)
	autoExplainComposedConfigure(t, s)
	for _, query := range []string{
		"UPDATE unit_rows SET i=i+10 WHERE CURRENT OF ae_role_cursor",
		"DELETE FROM unit_rows WHERE CURRENT OF ae_role_cursor",
	} {
		op := enginetest.TransactionOperation(t, s)
		op.OriginalSQL, op.SQLAvailable = query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
		}
		observation, _, retired := core.AutoExplainWitnessForTest(scope)
		records, _, shared := core.AutoExplainDMLRecordsForTest(scope)
		if len(records) != 1 || !shared || records[0].CommandActual().Rows != 0 {
			t.Fatal("positioned command root/presentation owner", records, shared)
		}
		locator, known := observation.DMLActual(exec.DMLCursorLocator)
		target, scanned := observation.DMLActual(exec.DMLTargetScan)
		if !known || !scanned || locator != (exec.Actual{Rows: 1, Loops: 1}) || target != (exec.Actual{Rows: 3, Loops: 1}) {
			t.Fatal("position lookup fabricated a single-tuple target scan", locator, target)
		}
		if err := scope.Finish(nil); err != nil || !retired() {
			t.Fatal("positioned observer did not retire", err)
		}
		if cursor.CurrentSource != source || cursor.CurrentIndex != index || core.CursorRoot(cursor) != root || core.CursorAuthority(cursor).Host != nil {
			t.Fatal("observation changed retained cursor identity/authority")
		}
	}
}
