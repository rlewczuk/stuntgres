package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerCore(t *testing.T) {
	eventTriggerScenario(t, []string{"et_core", "et_null"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		eventTriggerAuditSetup(t, p, b, c)
		eventTriggerFunction(t, p, b, c, "et_core_fn", 1, "RETURN;")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EVENT TRIGGER et_core ON ddl_command_start WHEN TAG IN ('create table','aLtEr TaBlE') EXECUTE PROCEDURE et_core_fn()", 0))
		run(fixture.Prepared(fixture.Exec("CREATE EVENT TRIGGER et_null ON ddl_command_end EXECUTE FUNCTION et_core_fn()", 0)))
		s := fixture.Select("SELECT oid,evtname,evtevent,evtowner,evtfoid,evtenabled,evttags FROM pg_catalog.pg_event_trigger WHERE false", []string{"oid", "evtname", "evtevent", "evtowner", "evtfoid", "evtenabled", "evttags"}, []string{"OID", "NAME", "NAME", "OID", "OID", "CHAR", "_TEXT"}, nil)
		run(s)
		run(fixture.Select("SELECT evtname,evtevent,evtenabled,evttags FROM pg_event_trigger WHERE evtname IN ('et_core','et_null') ORDER BY evtname", []string{"evtname", "evtevent", "evtenabled", "evttags"}, []string{"NAME", "NAME", "CHAR", "_TEXT"}, [][]any{{"et_core", "ddl_command_start", "O", `{"CREATE TABLE","ALTER TABLE"}`}, {"et_null", "ddl_command_end", "O", nil}}))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_roles r ON r.oid=e.evtowner WHERE e.evtname IN ('et_core','et_null') AND f.proname='et_core_fn' AND r.rolname=current_user AND e.oid>0", 2))
		run(fixture.ErrorDiagnostic("CREATE EVENT TRIGGER et_core ON ddl_command_start EXECUTE FUNCTION et_core_fn()", eventTriggerDiagnostic("42710", `event trigger "et_core" already exists`)))
		run(fixture.Exec("ALTER EVENT TRIGGER et_null DISABLE", 0))
		run(fixture.Exec("CREATE TABLE et_created(id integer NOT NULL)", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_core_fn", "ddl_command_start", "CREATE TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 1))
		// Replacement must retain the function identity, not a cached body/name.
		eventTriggerFunction(t, p, b, c, "et_core_fn", 7, "RETURN;")
		run(fixture.Prepared(fixture.Exec("ALTER TABLE et_created ADD COLUMN v text", 0)))
		run(eventTriggerAudit(eventTriggerRow(7, "et_core_fn", "ddl_command_start", "ALTER TABLE")))
		run(fixture.Exec("ALTER FUNCTION et_core_fn() RENAME TO et_renamed", 0))
		run(fixture.Exec("CREATE FUNCTION et_core_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'wrong identity'; END$$", 0))
		run(fixture.Exec("DELETE FROM et_audit", 1))
		run(fixture.Exec("ALTER TABLE et_created ADD COLUMN w integer", 0))
		run(eventTriggerAudit(eventTriggerRow(7, "et_core_fn", "ddl_command_start", "ALTER TABLE")))
		// RESTRICT/CASCADE exercise the real function dependency graph.
		dependency := eventTriggerDiagnostic("2BP01", "cannot drop function et_renamed() because other objects depend on it")
		dependency.Detail = "event trigger et_core depends on function et_renamed()\nevent trigger et_null depends on function et_renamed()"
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP FUNCTION et_renamed() RESTRICT", dependency))
		run(fixture.Exec("DROP FUNCTION et_renamed() CASCADE", 0))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname IN ('et_core','et_null')", 0))
	})
}

func TestReferenceEventTriggerFilters(t *testing.T) {
	eventTriggerScenario(t, []string{"et_filter", "et_future"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		eventTriggerAuditSetup(t, p, b, c)
		eventTriggerFunction(t, p, b, c, "et_filter_fn", 1, "RETURN;")
		eventTriggerCreate(t, p, b, c, "et_filter", "ddl_command_start", "'create table','ALTER TABLE'", "et_filter_fn")
		eventTriggerCreate(t, p, b, c, "et_future", "ddl_command_start", "'CREATE PUBLICATION'", "et_filter_fn")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE et_filtered(id integer)", 0))
		run(fixture.Prepared(fixture.Exec("ALTER TABLE et_filtered ADD COLUMN v text", 0)))
		s := eventTriggerAudit(eventTriggerRow(1, "et_filter_fn", "ddl_command_start", "ALTER TABLE"), eventTriggerRow(1, "et_filter_fn", "ddl_command_start", "CREATE TABLE"))
		run(s)
		run(fixture.Exec("DELETE FROM et_audit", 2))
		run(fixture.Exec("DROP TABLE et_filtered", 0))
		run(eventTriggerAudit())
	})
	for _, tc := range []struct{ name, suffix, code, message string }{
		{"event", "ON unknown_event EXECUTE FUNCTION et_empty()", "42601", `unrecognized event name "unknown_event"`},
		{"variable", "ON ddl_command_start WHEN WHAT IN ('CREATE TABLE') EXECUTE FUNCTION et_empty()", "42601", `unrecognized filter variable "what"`},
		{"duplicate", "ON ddl_command_start WHEN TAG IN ('CREATE TABLE') AND TAG IN ('ALTER TABLE') EXECUTE FUNCTION et_empty()", "42601", `filter variable "tag" specified more than once`},
		{"unknown", "ON ddl_command_start WHEN TAG IN ('NO SUCH COMMAND') EXECUTE FUNCTION et_empty()", "42601", `filter value "NO SUCH COMMAND" not recognized for filter variable "tag"`},
		{"excluded", "ON ddl_command_start WHEN TAG IN ('CREATE ROLE') EXECUTE FUNCTION et_empty()", "0A000", "event triggers are not supported for CREATE ROLE"},
		{"rewrite", "ON table_rewrite WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION et_empty()", "0A000", "event triggers are not supported for CREATE TABLE"},
		{"rewrite_unknown", "ON table_rewrite WHEN TAG IN ('nonsense') EXECUTE FUNCTION et_empty()", "0A000", "event triggers are not supported for nonsense"},
		{"excluded_spelling", "ON ddl_command_start WHEN TAG IN ('create role') EXECUTE FUNCTION et_empty()", "0A000", "event triggers are not supported for create role"},
		{"ascii_case_only", "ON ddl_command_start WHEN TAG IN ('create ındex') EXECUTE FUNCTION et_empty()", "42601", `filter value "create ındex" not recognized for filter variable "tag"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_bad"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION et_empty() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE EVENT TRIGGER et_bad "+tc.suffix, eventTriggerDiagnostic(tc.code, tc.message))
					s.ErrorStage = "execute"
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
}

func TestReferenceEventTriggerFrame(t *testing.T) {
	eventTriggerScenario(t, []string{"et_frame"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var records []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		run(fixture.Exec("CREATE FUNCTION et_frame_kind(value text) RETURNS text LANGUAGE SQL AS $$SELECT 'text'$$", 0))
		run(fixture.Exec("CREATE FUNCTION et_frame_kind(value varchar) RETURNS text LANGUAGE SQL AS $$SELECT 'varchar'$$", 0))
		run(fixture.Exec("CREATE FUNCTION et_frame_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE '%:%:%:%',TG_EVENT,TG_TAG,et_frame_kind(TG_EVENT),et_frame_kind(TG_TAG); RETURN; END$$", 0))
		eventTriggerCreate(t, p, b, c, "et_frame", "ddl_command_start", "'CREATE TABLE'", "et_frame_fn")
		records = nil
		run(fixture.Exec("CREATE TABLE et_frame_data(id integer)", 0))
		want := []fixture.Diagnostic{eventTriggerNotice("et_frame_fn", "ddl_command_start:CREATE TABLE:text:text")}
		if !reflect.DeepEqual(records, want) {
			t.Fatalf("frame notices: got %#v want %#v", records, want)
		}
		run(fixture.ErrorDiagnostic("SELECT et_frame_fn()", func() fixture.Diagnostic {
			d := eventTriggerDiagnostic("0A000", "trigger functions can only be called as triggers")
			d.Context = "compilation of PL/pgSQL function \"et_frame_fn\" near line 1"
			return d
		}()))
		run(fixture.Exec("CREATE OR REPLACE FUNCTION et_frame_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", 0))
		run(fixture.Prepared(fixture.Exec("CREATE TABLE et_fallthrough(id integer)", 0)))
		run(fixture.Exec("CREATE FUNCTION et_ordinary() RETURNS text LANGUAGE plpgsql AS $$DECLARE tg_event text := 'ordinary'; BEGIN RETURN tg_event; END$$", 0))
		run(fixture.Select("SELECT et_ordinary() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ordinary"}}))
	})
}

func TestReferenceEventTriggerOrdering(t *testing.T) {
	eventTriggerScenario(t, []string{"a_start", "z_start", "a_end", "z_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		eventTriggerAuditSetup(t, p, b, c)
		for i, name := range []string{"a_start", "z_start", "a_end", "z_end"} {
			eventTriggerFunction(t, p, b, c, name, i+1, "RETURN;")
		}
		for _, name := range []string{"z_end", "z_start", "a_end", "a_start"} {
			event := "ddl_command_start"
			if name == "z_end" || name == "a_end" {
				event = "ddl_command_end"
			}
			// Each later callback checks visibility of the prior one's write,
			// rather than relying on an ORDER BY to manufacture firing order.
			eventTriggerCreate(t, p, b, c, name, event, "'CREATE TABLE'", name)
		}
		for _, q := range []string{
			"CREATE OR REPLACE FUNCTION z_start() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM et_audit; IF n<>1 THEN RAISE EXCEPTION 'start order'; END IF; SELECT et_record(2,'z_start',TG_EVENT,TG_TAG) INTO n; END$$",
			"CREATE OR REPLACE FUNCTION a_end() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM et_audit; IF n<>2 THEN RAISE EXCEPTION 'end before start'; END IF; SELECT et_record(3,'a_end',TG_EVENT,TG_TAG) INTO n; END$$",
			"CREATE OR REPLACE FUNCTION z_end() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM et_audit; IF n<>3 THEN RAISE EXCEPTION 'end order'; END IF; SELECT et_record(4,'z_end',TG_EVENT,TG_TAG) INTO n; END$$",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_ordered(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit(
			eventTriggerRow(1, "a_start", "ddl_command_start", "CREATE TABLE"), eventTriggerRow(2, "z_start", "ddl_command_start", "CREATE TABLE"),
			eventTriggerRow(3, "a_end", "ddl_command_end", "CREATE TABLE"), eventTriggerRow(4, "z_end", "ddl_command_end", "CREATE TABLE")))
	})
}
