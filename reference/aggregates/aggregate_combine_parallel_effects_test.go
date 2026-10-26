package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The routine contexts below follow the released SQL execution error callback.
// PostgreSQL can report a transition from either participant; only its documented
// parallel-worker suffix is admitted as a second complete diagnostic variant.
// Released utility.c admits SQL SET/RESET before guc_funcs.c; its command-tag
// error precedes the named set_config guard in guc.c, as observed on PostgreSQL.
func TestReferenceAggregateCombineParallelEffects(t *testing.T) {
	for _, tc := range []struct {
		name, setup, transition, combine, final, command, routineContext string
		serialLog                                                        int64
		worker                                                           bool
	}{
		{"transition_insert", "", "BEGIN ATOMIC INSERT INTO ac_effect_log VALUES ($2); SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", "", "INSERT", "SQL function \"ac_effect_step\" statement 1", 5, true},
		{"nested_insert", "CREATE FUNCTION ac_effect_nested(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES ($1); SELECT $1; END", "BEGIN ATOMIC SELECT coalesce($1,0)+ac_effect_nested($2); END", "pg_catalog.int4pl", "", "INSERT", "SQL function \"ac_effect_nested\" statement 1\nSQL function \"ac_effect_step\" statement 1", 5, true},
		{"modifying_cte", "", "BEGIN ATOMIC WITH inserted AS (INSERT INTO ac_effect_log VALUES ($2) RETURNING v) SELECT coalesce($1,0)+v FROM inserted; END", "pg_catalog.int4pl", "", "SELECT", "SQL function \"ac_effect_step\" statement 1", 5, true},
		{"combine_insert", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES (88); SELECT $1+$2; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "INSERT", "SQL function \"ac_effect_merge\" statement 1", 1, false},
		{"final_insert", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ac_effect_log VALUES (88); SELECT $1; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "INSERT", "SQL function \"ac_effect_final\" statement 1", 2, false},
		{"combine_sequence", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer BEGIN ATOMIC SELECT $1+$2+nextval('ac_effect_seq')::integer; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "nextval()", "SQL function \"ac_effect_merge\" statement 1", 1, false},
		{"final_sequence", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer BEGIN ATOMIC SELECT $1+nextval('ac_effect_seq')::integer; END", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "nextval()", "SQL function \"ac_effect_final\" statement 1", 1, false},
		{"transition_setting_set", "", "LANGUAGE SQL AS $$SET application_name='escaped'; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_reset", "", "LANGUAGE SQL AS $$RESET application_name; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_reset_all", "", "LANGUAGE SQL AS $$RESET ALL; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_local", "", "LANGUAGE SQL AS $$SET LOCAL application_name='escaped'; SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='escaped'$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_default", "", "LANGUAGE SQL AS $$SET application_name TO DEFAULT; SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_sql", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_config", "", "LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT coalesce($1,0)+$2$$", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"transition_setting_config_local", "", "LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',TRUE); SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='escaped'$$", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_step\" statement 1", 1, true},
		{"nested_setting_config", "CREATE FUNCTION ac_effect_nested(integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+ac_effect_nested($2); END", "pg_catalog.int4pl", "", "setting_config", "SQL function \"ac_effect_nested\" statement 1\nSQL function \"ac_effect_step\" statement 1", 1, true},
		{"combine_setting_set", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer LANGUAGE SQL AS $$SET application_name='escaped'; SELECT $1+$2$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "setting_sql", "SQL function \"ac_effect_merge\" statement 1", 1, false},
		{"combine_setting_config", "CREATE FUNCTION ac_effect_merge(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1+$2$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "ac_effect_merge", "", "setting_config", "SQL function \"ac_effect_merge\" statement 1", 1, false},
		{"final_setting_reset", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$RESET application_name; SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "setting_sql", "SQL function \"ac_effect_final\" statement 1", 1, false},
		{"final_setting_config", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',FALSE); SELECT $1$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "setting_config", "SQL function \"ac_effect_final\" statement 1", 1, false},
		{"transition_setting_read", "", "LANGUAGE SQL AS $$SHOW application_name; SELECT coalesce($1,0)+$2 FROM pg_catalog.pg_settings WHERE name='application_name' AND setting='before'$$", "pg_catalog.int4pl", "", "read", "", 1, true},
		// Released inline_function rejects multiple commands: keep both frames.
		{"nested_deallocate_all", "", "LANGUAGE SQL AS $$SELECT 0; SELECT coalesce($1,0)+$2+ac_effect_clear()$$", "pg_catalog.int4pl", "", "DEALLOCATE ALL", "SQL function \"ac_effect_clear\" statement 1\nSQL function \"ac_effect_step\" statement 2", 1, true},
		{"final_deallocate", "CREATE FUNCTION ac_effect_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 0; SELECT $1+ac_effect_clear()$$", "BEGIN ATOMIC SELECT coalesce($1,0)+$2; END", "pg_catalog.int4pl", ",FINALFUNC=ac_effect_final", "DEALLOCATE", "SQL function \"ac_effect_clear\" statement 1\nSQL function \"ac_effect_final\" statement 2", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("SET application_name='before'", 0),
					fixture.Exec("CREATE TABLE ac_effect_log(v integer)", 0),
					fixture.Exec("INSERT INTO ac_effect_log VALUES (99)", 1),
					fixture.Exec("CREATE TABLE ac_effect_destination(v integer)", 0),
					fixture.Exec("INSERT INTO ac_effect_destination VALUES (77)", 1),
					fixture.Exec("CREATE SEQUENCE ac_effect_seq", 0),
					fixture.Exec("CREATE TABLE ac_effect_source(x integer,padding text)", 0),
					fixture.Exec("INSERT INTO ac_effect_source SELECT generate_series(1,16384),repeat('x',1024)", 16384))
				if tc.command == "DEALLOCATE ALL" || tc.command == "DEALLOCATE" {
					command := tc.command
					if command == "DEALLOCATE" {
						command += " ac_saved"
					}
					createAggregateRun(t, p, b, c, fixture.Exec("CREATE FUNCTION ac_effect_clear() RETURNS integer LANGUAGE SQL AS $$"+command+"; SELECT 0$$", 0))
				}
				if tc.setup != "" {
					createAggregateRun(t, p, b, c, fixture.Exec(tc.setup, 0))
				}
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION ac_effect_step(integer,integer) RETURNS integer "+tc.transition, 0),
					fixture.Exec("CREATE AGGREGATE ac_effect(integer) (SFUNC=ac_effect_step,STYPE=integer,INITCOND='0',COMBINEFUNC="+tc.combine+tc.final+",PARALLEL=SAFE)", 0),
					fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
				if b.PG {
					// Physical PostgreSQL planning controls, not native feature knobs.
					createAggregateRun(t, p, b, c,
						fixture.Exec("SET min_parallel_table_scan_size=0", 0),
						fixture.Exec("SET parallel_setup_cost=0", 0),
						fixture.Exec("SET parallel_tuple_cost=0", 0))
					planRows, err := c.QueryContext(p.Ctx, "EXPLAIN SELECT ac_effect(x) AS v FROM ac_effect_source WHERE x<=4")
					if err != nil {
						t.Fatal(err)
					}
					var plan strings.Builder
					for planRows.Next() {
						var line string
						if err := planRows.Scan(&line); err != nil {
							planRows.Close()
							t.Fatal(err)
						}
						plan.WriteString(line)
						plan.WriteByte('\n')
					}
					err = planRows.Err()
					planRows.Close()
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(plan.String(), "Partial Aggregate") || !strings.Contains(plan.String(), "Finalize Aggregate") {
						t.Fatal("oracle did not admit actual partial aggregation: ", plan.String())
					}
				}
				// SQL PREPARE is distinct from the database/sql held query below.
				createAggregateRun(t, p, b, c,
					fixture.Exec("PREPARE ac_saved AS SELECT 42 AS v", 0),
					fixture.Select("EXECUTE ac_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				const query = "SELECT ac_effect(x) AS v FROM ac_effect_source WHERE x<=$1"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				checkFailure := func(t *testing.T, rows *sql.Rows, failure error) {
					if !b.PG && rows != nil {
						rows.Close()
						t.Fatal("native published rows on parallel failure")
					}
					if failure == nil && rows != nil {
						if rows.Next() {
							rows.Close()
							t.Fatal("parallel failure published an aggregate row")
						}
						failure = rows.Err()
					}
					if rows != nil {
						rows.Close()
					}
					want := fixture.ControlsReferenceDiagnostic("25000", "cannot execute "+tc.command+" during a parallel operation")
					if tc.command == "setting_sql" {
						want.Message = "cannot execute SET during a parallel operation"
						switch tc.name {
						case "transition_setting_reset", "transition_setting_reset_all", "final_setting_reset":
							want.Message = "cannot execute RESET during a parallel operation"
						}
					}
					if tc.command == "setting_config" {
						want.Message = "parameter \"application_name\" cannot be set during a parallel operation"
					}
					want.Context = tc.routineContext
					if b.PG && tc.worker {
						actual := fixture.ExtractDiagnostic(t, failure)
						if actual.Context == tc.routineContext+"\nparallel worker" {
							want.Context += "\nparallel worker"
						}
					}
					fixture.AssertDiagnostic(t, failure, &want)
				}
				for _, route := range []string{"direct", "fresh", "held"} {
					t.Run(route, func(t *testing.T) {
						if tc.command == "read" {
							s := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}, 4)
							s.Prepared = route == "fresh"
							if route == "held" {
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							} else {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							}
							return
						}
						var rows *sql.Rows
						var failure error
						switch route {
						case "direct":
							rows, failure = c.QueryContext(p.Ctx, query, 4)
						case "fresh":
							fresh, err := c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer fresh.Close()
							rows, failure = fresh.QueryContext(p.Ctx, 4)
						case "held":
							rows, failure = stmt.QueryContext(p.Ctx, 4)
						}
						checkFailure(t, rows, failure)
						createAggregateRun(t, p, b, c, fixture.Select("EXECUTE ac_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
						createAggregateRun(t, p, b, c,
							fixture.Select("SELECT v FROM ac_effect_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}),
							fixture.Select("SELECT v FROM ac_effect_destination", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
						createAggregateRun(t, p, b, c,
							fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"before"}}),
							fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"before"}}))
					})
				}
				createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT nextval('ac_effect_seq') AS v", int64(1)), aggregateCombineValue("SELECT setval('ac_effect_seq',1,FALSE) AS v", int64(1)), fixture.Exec("SET max_parallel_workers_per_gather=0", 0))
				value := int64(10)
				if tc.name == "final_sequence" {
					value = 11
				}
				s := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{value}}, 4)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				if tc.command == "DEALLOCATE ALL" || tc.command == "DEALLOCATE" {
					_, err := c.ExecContext(p.Ctx, "EXECUTE ac_saved")
					want := fixture.ControlsReferenceDiagnostic("26000", "prepared statement \"ac_saved\" does not exist")
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					createAggregateRun(t, p, b, c, fixture.Select("EXECUTE ac_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				}
				createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT count(*) AS v FROM ac_effect_log", tc.serialLog))
				logRows := [][]any{{int64(99)}}
				if tc.serialLog == 5 {
					logRows = [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(99)}}
				}
				if tc.serialLog == 2 {
					logRows = [][]any{{int64(88)}, {int64(99)}}
				}
				createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM ac_effect_log ORDER BY v", []string{"v"}, []string{"INT4"}, logRows))
				if tc.name == "combine_insert" {
					createAggregateRun(t, p, b, c,
						fixture.Select("SELECT ac_effect_merge(1,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}),
						fixture.Select("SELECT v FROM ac_effect_log ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(88)}, {int64(99)}}))
				}
				if tc.name == "combine_sequence" {
					createAggregateRun(t, p, b, c, fixture.Select("SELECT ac_effect_merge(1,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
				}
				wantSetting := "before"
				switch tc.name {
				case "transition_setting_set", "transition_setting_config", "nested_setting_config", "final_setting_config":
					wantSetting = "escaped"
				case "transition_setting_reset", "transition_setting_reset_all", "transition_setting_default", "final_setting_reset":
					wantSetting = ""
				case "combine_setting_set", "combine_setting_config":
					createAggregateRun(t, p, b, c, fixture.Select("SELECT ac_effect_merge(1,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
					wantSetting = "escaped"
				}
				createAggregateRun(t, p, b, c,
					fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{wantSetting}}),
					fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{wantSetting}}))
			})
		})
	}
}
