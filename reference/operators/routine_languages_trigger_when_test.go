package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 ExecBRInsertTriggers calls TriggerEnabled (including WHEN) before
// ExecCallTriggerFunc: skipped rows must not compile a deferred trigger body.
func TestReferenceRoutineLanguagesTriggerWhen(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_trigger_when CASCADE")()
				userOperatorExec(t, p, b, c,
					"CREATE TRUSTED LANGUAGE rl_trigger_when"+routineLanguageCall,
					"CREATE FUNCTION rl_trigger_when_fn() RETURNS trigger LANGUAGE rl_trigger_when AS 'BEGIN RETURN NEW;'",
					"CREATE TABLE rl_trigger_when_data(v integer)",
					"CREATE TRIGGER rl_trigger_when_before BEFORE INSERT ON rl_trigger_when_data FOR EACH ROW WHEN (NEW.v > 0) EXECUTE FUNCTION rl_trigger_when_fn()")
				for _, q := range []string{
					"INSERT INTO rl_trigger_when_data VALUES (-1)",
					"INSERT INTO rl_trigger_when_data VALUES (NULL)",
				} {
					s := fixture.Exec(q, 1)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				rows := fixture.Select("SELECT v FROM rl_trigger_when_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}, {nil}})
				rows.Prepared = prepared
				rows.CheckMetadata = parametersMetadataNullability(b, "v", "INT4", !b.PG, !b.PG)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, rows)
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				d.InternalPosition, d.InternalQuery = "18", "BEGIN RETURN NEW;"
				d.Context = "compilation of PL/pgSQL function \"rl_trigger_when_fn\" near line 1"
				failure := fixture.ErrorDiagnostic("INSERT INTO rl_trigger_when_data VALUES (-2),(1),(NULL)", d)
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
				// The skipped first row in the failed statement must not survive.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, rows)
				catalog := fixture.Select("SELECT p.prosrc,p.pronargs,l.lanname,l.lanvalidator=0 AS deferred FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='rl_trigger_when_fn'",
					[]string{"prosrc", "pronargs", "lanname", "deferred"}, []string{"TEXT", "INT2", "NAME", "BOOL"}, [][]any{{"BEGIN RETURN NEW;", int64(0), "rl_trigger_when", true}})
				catalog.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, catalog)
				// A failed reached compilation must not poison subsequent skips.
				skipped := fixture.Exec("INSERT INTO rl_trigger_when_data VALUES (-3),(NULL)", 2)
				skipped.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, skipped)
				rows.Rows = [][]any{{int64(-3)}, {int64(-1)}, {nil}, {nil}}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, rows)
			})
		})
	}
}
