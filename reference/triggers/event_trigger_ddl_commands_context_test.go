package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const ddlCommandsContextMessage = "pg_event_trigger_ddl_commands() can only be called in an event trigger function"

// Expectations come from REL_18_6 event_trigger.c: the guard tests complete-query
// collection state, not TG_EVENT. In particular start-only and start+end differ.
func TestReferenceEventTriggerDDLCommandsContext(t *testing.T) {
	t.Run("outside", func(t *testing.T) {
		eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, query := range []string{"SELECT pg_event_trigger_ddl_commands()", "SELECT * FROM pg_event_trigger_ddl_commands()"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, eventTriggerDiagnostic("39P03", ddlCommandsContextMessage))
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		})
	})
	for _, inventory := range []string{"none", "start_only", "start_end", "start_unmatched_end", "sql_drop", "rewrite_end"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", inventory, prepared), func(t *testing.T) {
				names := []string{"et_dc_start", "et_dc_end", "et_dc_drop", "et_dc_rewrite"}
				eventTriggerScenario(t, names, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{
						"CREATE TABLE et_dc_context(phase text,tag text,n bigint)",
						"CREATE TABLE et_dc_target(i integer)",
						"INSERT INTO et_dc_target VALUES(7)",
						"CREATE FUNCTION et_dc_count() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_context SELECT TG_EVENT,TG_TAG,count(*) FROM pg_event_trigger_ddl_commands(); END$$",
					} {
						count := int64(0)
						if q == "INSERT INTO et_dc_target VALUES(7)" {
							count = 1
						}
						run(fixture.Exec(q, count))
					}
					install := func(name, phase, tag string) { eventTriggerCreate(t, p, b, c, name, phase, tag, "et_dc_count") }
					query := "CREATE TABLE et_dc_created(i integer)"
					var want [][]any
					switch inventory {
					case "start_only":
						install("et_dc_start", "ddl_command_start", "'CREATE TABLE'")
					case "start_end":
						install("et_dc_start", "ddl_command_start", "'CREATE TABLE'")
						install("et_dc_end", "ddl_command_end", "'CREATE TABLE'")
						want = [][]any{{"ddl_command_end", "CREATE TABLE", int64(1)}, {"ddl_command_start", "CREATE TABLE", int64(0)}}
					case "start_unmatched_end":
						install("et_dc_start", "ddl_command_start", "'CREATE TABLE'")
						install("et_dc_end", "ddl_command_end", "'CREATE VIEW'")
						want = [][]any{{"ddl_command_start", "CREATE TABLE", int64(0)}}
					case "sql_drop":
						install("et_dc_drop", "sql_drop", "'DROP TABLE'")
						query = "DROP TABLE et_dc_target"
						want = [][]any{{"sql_drop", "DROP TABLE", int64(0)}}
					case "rewrite_end":
						install("et_dc_rewrite", "table_rewrite", "'ALTER TABLE'")
						install("et_dc_end", "ddl_command_end", "'ALTER TABLE'")
						query = "ALTER TABLE et_dc_target ALTER COLUMN i TYPE bigint"
						want = [][]any{{"ddl_command_end", "ALTER TABLE", int64(1)}, {"table_rewrite", "ALTER TABLE", int64(0)}}
					}
					step := fixture.Exec(query, 0)
					if inventory == "start_only" {
						d := eventTriggerDiagnostic("39P03", ddlCommandsContextMessage)
						d.Context = "SQL statement \"INSERT INTO public.et_dc_context SELECT TG_EVENT,TG_TAG,count(*) FROM pg_event_trigger_ddl_commands()\"\nPL/pgSQL function et_dc_count() line 1 at SQL statement"
						step = fixture.ErrorDiagnostic(query, d)
						step.ErrorStage = "execute"
					}
					step.Prepared = prepared
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatalf("Prepare must not inspect event state: %v", err)
						}
						defer stmt.Close()
						run(fixture.Select("SELECT phase,tag,n FROM et_dc_context", []string{"phase", "tag", "n"}, []string{"TEXT", "TEXT", "INT8"}, nil))
						step.Prepared = false
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, step)
					} else {
						run(step)
					}
					run(fixture.Select("SELECT phase,tag,n FROM et_dc_context ORDER BY phase", []string{"phase", "tag", "n"}, []string{"TEXT", "TEXT", "INT8"}, want))
					if inventory == "start_only" {
						run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_created'", 0))
					}
				})
			})
		}
	}
}
