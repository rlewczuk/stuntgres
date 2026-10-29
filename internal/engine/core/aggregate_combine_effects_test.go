package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Observe real support entry without replacing the provider or its state.
type aggregateEffectHost struct {
	*core.StatementHost
	workers atomic.Int64
	leaders atomic.Int64
}

func (h *aggregateEffectHost) InvokeAggregateRoutine(ctx context.Context, d *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, owner *catalog.Procedure, collation sqltype.CollationRef) (sqltype.Value, error) {
	if !exec.InParallelOperation(ctx) {
		return sqltype.Value{}, errors.New("actual support lost parallel operation context")
	}
	if d.Name() == "ac_effect_step" {
		if !exec.InParallelWorker(ctx) {
			return sqltype.Value{}, errors.New("transition lost real worker identity")
		}
		h.workers.Add(1)
	} else {
		if exec.InParallelWorker(ctx) {
			return sqltype.Value{}, errors.New("leader support inherited worker identity")
		}
		h.leaders.Add(1)
	}
	return h.StatementHost.InvokeAggregateRoutine(ctx, d, frame, usage, owner, collation)
}

func TestAggregateCombineParallelEffects(t *testing.T) {
	for _, tc := range []struct {
		name, setup, transition, combine, final, message, diagnosticContext string
		serialLog                                                           int64
	}{
		{"transition_insert", "", "BEGIN ATOMIC INSERT INTO ac_effect_log VALUES ($2); SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", "", "INSERT", "SQL function \"ac_effect_step\" statement 1", 5},
		{"nested_insert", "CREATE FUNCTION ac_effect_nested(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES ($1); SELECT $1; END", "BEGIN ATOMIC SELECT coalesce($1,0)+ac_effect_nested($2); END", "pg_catalog.int4pl", "", "INSERT", "SQL function \"ac_effect_nested\" statement 1\nSQL function \"ac_effect_step\" statement 1", 5},
		{"modifying_cte", "", "BEGIN ATOMIC WITH inserted AS (INSERT INTO ac_effect_log VALUES ($2) RETURNING v) SELECT coalesce($1,0)+v FROM inserted; END", "pg_catalog.int4pl", "", "SELECT", "SQL function \"ac_effect_step\" statement 1", 5},
		{"combine_insert", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES (88); SELECT $1+$2; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "INSERT", "SQL function \"ac_effect_merge\" statement 1", 1},
		{"final_insert", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES (88); SELECT $1; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "INSERT", "SQL function \"ac_effect_final\" statement 1", 2},
		{"combine_sequence", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer BEGIN ATOMIC SELECT $1+$2+nextval('ac_effect_seq')::integer; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "nextval()", "SQL function \"ac_effect_merge\" statement 1", 1},
		{"final_sequence", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer BEGIN ATOMIC SELECT $1+nextval('ac_effect_seq')::integer; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "nextval()", "SQL function \"ac_effect_final\" statement 1", 1},
		{"transition_setting_set", "", "LANGUAGE SQL AS $$SET application_name='escaped'; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_reset", "", "LANGUAGE SQL AS $$RESET application_name; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_reset_all", "", "LANGUAGE SQL AS $$RESET ALL; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_local", "", "LANGUAGE SQL AS $$SET LOCAL application_name='escaped'; SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='escaped'$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_default", "", "LANGUAGE SQL AS $$SET application_name TO DEFAULT; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_config", "", "LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_step\" statement 1", 1},
		{"transition_setting_config_local", "", "LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',TRUE); SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='escaped'$$", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_step\" statement 1", 1},
		{"nested_setting_config", "CREATE FUNCTION ac_effect_nested(integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+ac_effect_nested($2); END", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_nested\" statement 1\nSQL function \"ac_effect_step\" statement 1", 1},
		{"combine_setting_set", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer LANGUAGE SQL AS $$SET application_name='escaped'; SELECT $1+$2$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "setting_sql", "SQL function \"ac_effect_merge\" statement 1", 1},
		{"combine_setting_config", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1+$2$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "setting_config", "SQL function \"ac_effect_merge\" statement 1", 1},
		{"final_setting_reset", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$RESET application_name; SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "setting_sql", "SQL function \"ac_effect_final\" statement 1", 1},
		{"final_setting_config", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "setting_config", "SQL function \"ac_effect_final\" statement 1", 1},
		{"transition_setting_read", "", "LANGUAGE SQL AS $$SHOW application_name; SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='before'$$", "pg_catalog.int4pl", "", "read", "", 1},
		// Two-command wrappers cannot be SQL-inlined: retain both real frames.
		{"nested_deallocate_all", "", "LANGUAGE SQL AS $$SELECT 0; SELECT coalesce($1,0)+$2+ac_effect_clear()$$", "pg_catalog.int4pl", "", "DEALLOCATE ALL", "SQL function \"ac_effect_clear\" statement 1\nSQL function \"ac_effect_step\" statement 2", 1},
		{"final_deallocate", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 0; SELECT $1+ac_effect_clear()$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "DEALLOCATE", "SQL function \"ac_effect_clear\" statement 1\nSQL function \"ac_effect_final\" statement 2", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			for _, sql := range []string{"CREATE TABLE ac_effect_log(v integer)", "INSERT INTO ac_effect_log VALUES (99)", "CREATE TABLE ac_effect_source(x integer)", "INSERT INTO ac_effect_source VALUES (1),(2),(3),(4)", "CREATE SEQUENCE ac_effect_seq"} {
				enginetest.Run(t, e, sql)
			}
			if tc.message == "DEALLOCATE ALL" || tc.message == "DEALLOCATE" {
				command := tc.message
				if command == "DEALLOCATE" {
					command += " ac_saved"
				}
				enginetest.Run(t, e, "CREATE FUNCTION ac_effect_clear() RETURNS integer LANGUAGE SQL AS $$"+command+"; SELECT 0$$")
			}
			if tc.setup != "" {
				enginetest.Run(t, e, tc.setup)
			}
			enginetest.Run(t, e, "CREATE FUNCTION ac_effect_step(integer,integer) RETURNS integer "+tc.transition)
			enginetest.Run(t, e, "CREATE AGGREGATE ac_effect(integer) (SFUNC=ac_effect_step,STYPE=integer,INITCOND='0',COMBINEFUNC="+tc.combine+tc.final+",PARALLEL=SAFE)")
			captured := enginetest.Run(t, e, "SELECT * FROM ac_effect_source").Rows.(*exec.FinalRows)
			source, err := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
			captured.Close()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.Close)
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(done)
			if _, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "SET application_name='before'"), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "PREPARE ac_saved AS SELECT 42 AS v"), nil); err != nil {
				t.Fatal(err)
			}
			registry := core.PayloadOf(op.Session).PreparedSQL
			saved := registry.Lookup("ac_saved")
			if saved == nil {
				t.Fatal("real SQL PREPARE did not publish ac_saved")
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			finished := false
			t.Cleanup(func() {
				if !finished {
					_ = scope.Finish(nil)
				}
			})
			const query = "SELECT ac_effect(x) AS v FROM ac_effect_source"
			raw := enginetest.Statement(t, query)
			if err := scope.Gate(raw); err != nil {
				t.Fatal(err)
			}
			if err := scope.AcquireRelation(raw, true); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, bindErr := e.AuthorizationLocked(op)
			var bound *plan.Query
			if bindErr == nil {
				binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, source.Ref, scope.Input, nil, nil)
				bindErr, bound = failure, binding.Query
				scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
			}
			e.Mu.Unlock()
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			host := &aggregateEffectHost{StatementHost: core.HostOf(scope).(*core.StatementHost)}
			source.Host = host
			node, eligible := exec.ParallelAggregatePlan(bound.Root, source)
			if !eligible {
				t.Fatal("effectful declared SAFE aggregate silently fell back")
			}
			store := op.Session.Settings
			store.Mu.Lock()
			values, typed, defaults, defaultTyped := maps.Clone(store.Values), maps.Clone(store.Typed), maps.Clone(store.Defaults), maps.Clone(store.DefaultTyped)
			actions, placeholders, providers, roleAction := maps.Clone(store.Actions), maps.Clone(store.Placeholders), maps.Clone(store.ProviderDefaults), store.RoleAction
			isolation, readOnly, deferrable, ended := store.CurrentIsolation, store.CurrentReadOnly, store.CurrentDeferrable, store.Ended
			store.Mu.Unlock()
			rows, count, failure := exec.FinalizeParallelAggregate(op.Context(), bound.Root, node, source, nil, 2)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25000", Message: "cannot execute " + tc.message + " during a parallel operation", Context: tc.diagnosticContext}
			if tc.message == "setting_sql" {
				want.Message = "cannot execute SET during a parallel operation"
				switch tc.name {
				case "transition_setting_reset", "transition_setting_reset_all", "final_setting_reset":
					want.Message = "cannot execute RESET during a parallel operation"
				}
			}
			if tc.message == "setting_config" {
				want.Message = "parameter \"application_name\" cannot be set during a parallel operation"
			}
			var diagnostic *sqlstate.Error
			if tc.message == "read" {
				if failure != nil || count != 1 || rows == nil {
					if rows != nil {
						rows.Close()
					}
					t.Fatal(rows, count, failure)
				}
				values := make([]sqltype.Value, len(rows.Schema()))
				present, err := rows.Next(op.Context(), values)
				if err != nil || !present {
					rows.Close()
					t.Fatal(present, err)
				}
				value, err := values[0].Integer()
				if err != nil || value != 10 {
					rows.Close()
					t.Fatal(value, err)
				}
				present, err = rows.Next(op.Context(), values)
				rows.Close()
				if err != nil || present {
					t.Fatal(present, err)
				}
			} else if rows != nil || count != 0 || !errors.As(failure, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				if rows != nil {
					rows.Close()
				}
				t.Fatalf("parallel outcome: rows=%v count=%d error=%#v want=%#v", rows, count, diagnostic, want)
			}
			if source.View != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 || core.ScopeAggregateInvocationContext(scope) != nil {
				t.Fatal("failed operation retained source or routine context")
			}
			if host.workers.Load() == 0 {
				t.Fatal("production worker transition was not reached")
			}
			if (tc.combine != "pg_catalog.int4pl" || tc.final != "") && host.leaders.Load() == 0 {
				t.Fatal("production leader support was not reached")
			}
			if exec.InParallelOperation(op.Context()) || exec.InParallelWorker(op.Context()) {
				t.Fatal("operation marker leaked to borrowed owner")
			}
			store.Mu.Lock()
			unchanged := reflect.DeepEqual(store.Values, values) && reflect.DeepEqual(store.Typed, typed) && reflect.DeepEqual(store.Defaults, defaults) && reflect.DeepEqual(store.DefaultTyped, defaultTyped) && reflect.DeepEqual(store.Actions, actions) && reflect.DeepEqual(store.Placeholders, placeholders) && reflect.DeepEqual(store.ProviderDefaults, providers) && reflect.DeepEqual(store.RoleAction, roleAction)
			unchanged = unchanged && store.CurrentIsolation == isolation && store.CurrentReadOnly == readOnly && store.CurrentDeferrable == deferrable && store.Ended == ended
			store.Mu.Unlock()
			if !unchanged {
				t.Fatal("parallel support installed or journaled a settings mutation before Finish")
			}
			if registry.Lookup("ac_saved") != saved {
				t.Fatal("parallel support changed real SQL prepared definition before Finish")
			}
			_ = scope.Finish(failure)
			finished = true
			run := func(sql string) core.Result {
				r, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, sql), nil)
				if err != nil {
					t.Fatal(sql, err)
				}
				if r.Rows != nil {
					t.Cleanup(func() { r.Rows.Close() })
				}
				return r
			}
			assertSetting := func(want string) {
				for _, sql := range []string{"SHOW application_name", "SELECT current_setting('application_name')"} {
					got := enginetest.Tuples(run(sql))
					if len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
						t.Fatal(sql, got, want)
					}
				}
			}
			assertSetting("before")
			assertInteger := func(sql string, want int64) {
				got := enginetest.Tuples(run(sql))
				if len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
					t.Fatal(sql, got, want)
				}
			}
			assertInteger("EXECUTE ac_saved", 42)
			assertInteger("SELECT count(*) FROM ac_effect_log WHERE v=99", 1)
			assertInteger("SELECT count(*) FROM ac_effect_log WHERE v<>99", 0)
			assertInteger("SELECT nextval('ac_effect_seq')", 1)
			run("SELECT setval('ac_effect_seq',1,FALSE)")
			run("SET max_parallel_workers_per_gather=0")
			serialValue := int64(10)
			if tc.name == "final_sequence" {
				serialValue = 11
			}
			assertInteger(query, serialValue)
			if tc.message == "DEALLOCATE ALL" || tc.message == "DEALLOCATE" {
				if registry.Lookup("ac_saved") != nil {
					t.Fatal("serial deallocation retained real SQL prepared definition")
				}
				r, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "EXECUTE ac_saved"), nil)
				if r.Rows != nil {
					r.Rows.Close()
					t.Fatal("removed SQL prepared statement published rows")
				}
				var diagnostic *sqlstate.Error
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "26000", Message: "prepared statement \"ac_saved\" does not exist"}
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatalf("serial deallocation did not remove SQL prepared name: %#v want %#v", diagnostic, want)
				}
			} else {
				assertInteger("EXECUTE ac_saved", 42)
			}
			assertInteger("SELECT count(*) FROM ac_effect_log", tc.serialLog)
			wantLog := []int64{99}
			if tc.serialLog == 5 {
				wantLog = []int64{1, 2, 3, 4, 99}
			}
			if tc.serialLog == 2 {
				wantLog = []int64{88, 99}
			}
			gotLog := enginetest.Tuples(run("SELECT v FROM ac_effect_log ORDER BY v"))
			if len(gotLog) != len(wantLog) {
				t.Fatal(gotLog, wantLog)
			}
			for i, want := range wantLog {
				if len(gotLog[i]) != 1 || gotLog[i][0] != want {
					t.Fatal(gotLog, wantLog)
				}
			}
			if tc.name == "combine_insert" {
				assertInteger("SELECT ac_effect_merge(1,2)", 3)
				assertInteger("SELECT count(*) FROM ac_effect_log WHERE v=88", 1)
			}
			if tc.name == "combine_sequence" {
				assertInteger("SELECT ac_effect_merge(1,2)", 4)
			}
			wantSetting := "before"
			switch tc.name {
			case "transition_setting_set", "transition_setting_config", "nested_setting_config", "final_setting_config":
				wantSetting = "escaped"
			case "transition_setting_reset", "transition_setting_reset_all", "transition_setting_default", "final_setting_reset":
				wantSetting = ""
			case "combine_setting_set", "combine_setting_config":
				assertInteger("SELECT ac_effect_merge(1,2)", 3)
				wantSetting = "escaped"
			}
			assertSetting(wantSetting)
		})
	}
}
