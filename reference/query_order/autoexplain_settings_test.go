package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoExplainLoad(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		const absent = "SELECT current_setting('auto_explain.log_analyze', true) AS v"
		run(fixture.Select(absent, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		const load = "LOAD 'auto_explain'"
		held, err := c.PrepareContext(p.Ctx, load)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		// Preparing the utility must not install executor hooks or typed GUCs.
		run(fixture.Select(absent, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Exec("SET auto_explain.log_analyze = 'true'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: load}, b.Recorder, fixture.Exec(load, 0))
		run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"on"}}))
		for _, alias := range []string{"auto_explain", "auto_explain.so", "$libdir/auto_explain", "$libdir/auto_explain.so"} {
			run(fixture.Exec("LOAD '"+alias+"'", 0))
			run(fixture.Prepared(fixture.Exec("LOAD '"+alias+"'", 0)))
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='auto_explain'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		autoExplainQuiet(t, notices)
		autoExplainConfigure(t, p, b, c, notices)
		// Repeated activation must not register multiple reports for one executor.
		run(fixture.Exec(load, 0))
		const query = "SELECT 7::integer AS v"
		run(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		autoExplainJSON(t, notices, query, nil, true, 1)
	})
}

func TestReferenceAutoExplainSettings(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("LOAD 'auto_explain'", 0))
		for _, setting := range []struct{ name, value string }{
			{"log_min_duration", "-1"},
			{"log_parameter_max_length", "-1"},
			{"log_analyze", "off"},
			{"log_verbose", "off"},
			{"log_buffers", "off"},
			{"log_wal", "off"},
			{"log_timing", "on"},
			{"log_triggers", "off"},
			{"log_nested_statements", "off"},
			{"log_format", "text"},
			{"sample_rate", "1"},
			{"log_level", "log"},
		} {
			name := "auto_explain." + setting.name
			run(fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{setting.value}}))
			run(fixture.Prepared(fixture.Select("SELECT current_setting('"+name+"') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{setting.value}})))
		}
		for _, setting := range []struct{ name, input, output string }{
			{"log_min_duration", "'2s'", "2s"},
			{"log_min_duration", "2147483647", "2147483647ms"},
			{"log_parameter_max_length", "2147483647", "2147483647B"},
			{"sample_rate", "0", "0"},
			{"sample_rate", "0.5", "0.5"},
			{"log_level", "debug", "debug2"},
		} {
			name := "auto_explain." + setting.name
			run(fixture.Exec("SET "+name+" = "+setting.input, 0))
			run(fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{setting.output}}))
		}
		run(fixture.Exec("RESET auto_explain.log_min_duration", 0))
		run(fixture.Exec("SET auto_explain.sample_rate = 0.5", 0))
		run(fixture.Select("SELECT set_config('auto_explain.sample_rate', NULL, false) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}}))
		run(fixture.Select("SELECT current_setting(NULL::text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		for _, name := range []string{"log_analyze", "log_verbose", "log_buffers", "log_wal", "log_timing", "log_triggers", "log_nested_statements"} {
			run(fixture.Exec("SET auto_explain."+name+" = true", 0))
			run(fixture.Select("SHOW auto_explain."+name, []string{"auto_explain." + name}, []string{"TEXT"}, [][]any{{"on"}}))
			run(fixture.Exec("SET auto_explain."+name+" = DEFAULT", 0))
		}
		autoExplainQuiet(t, notices)
	})
	autoExplainPromotionCases(t)
}

func TestReferenceAutoExplainDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ae_initial_reader"})
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("LOAD 'auto_explain'", 0))
		for _, test := range []struct {
			query, message, hint, detail string
		}{
			{"SET auto_explain.log_analyze = 'bogus'", `parameter "auto_explain.log_analyze" requires a Boolean value`, "", ""},
			{"SET auto_explain.log_format = 'bogus'", `invalid value for parameter "auto_explain.log_format": "bogus"`, "Available values: text, xml, json, yaml.", ""},
			{"SET auto_explain.log_level = 'bogus'", `invalid value for parameter "auto_explain.log_level": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, info, notice, warning, log.", ""},
			{"SET auto_explain.sample_rate = 2", `2 is outside the valid range for parameter "auto_explain.sample_rate" (0 .. 1)`, "", ""},
			{"SET auto_explain.log_min_duration = -2", `-2 ms is outside the valid range for parameter "auto_explain.log_min_duration" (-1 ms .. 2147483647 ms)`, "", ""},
			{"SET auto_explain.log_parameter_max_length = -2", `-2 B is outside the valid range for parameter "auto_explain.log_parameter_max_length" (-1 B .. 2147483647 B)`, "", ""},
			{"SET auto_explain.typo = 'x'", `invalid configuration parameter name "auto_explain.typo"`, "", `"auto_explain" is a reserved prefix.`},
		} {
			code := "22023"
			if test.detail != "" {
				code = "42602"
			}
			d := fixture.ControlsReferenceDiagnostic(code, test.message)
			d.Hint, d.Detail = test.hint, test.detail
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(test.query, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
		}
		// A failed candidate does not replace any prior typed value.
		run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"off"}}))
		run(fixture.Select("SHOW auto_explain.sample_rate", []string{"auto_explain.sample_rate"}, []string{"TEXT"}, [][]any{{"1"}}))
		s := fixture.ErrorDiagnostic("SELECT set_config(NULL, 'x', false)", fixture.ControlsReferenceDiagnostic("22004", "SET requires parameter name"))
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Exec("CREATE ROLE ae_initial_reader", 0))
		run(fixture.Exec("SET ROLE ae_initial_reader", 0))
		for _, test := range []struct{ query, message string }{
			{"LOAD 'auto_explain'", `access to library "auto_explain" is not allowed`},
			{"SET auto_explain.log_analyze = 'bogus'", `permission denied to set parameter "auto_explain.log_analyze"`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(test.query, fixture.ControlsReferenceDiagnostic("42501", test.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		autoExplainQuiet(t, notices)
	})
	autoExplainDomainCases(t)
	autoExplainPreparedSettingsAuthorityCases(t)
}

func autoExplainPreparedSettingsAuthorityCases(t *testing.T) {
	t.Run("held_assignment_and_load_current_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ae_held_reader"})
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE ae_held_reader", 0))
			const assignment = "SET auto_explain.log_analyze=true"
			const load = "LOAD 'auto_explain'"
			set, err := c.PrepareContext(p.Ctx, assignment)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			activate, err := c.PrepareContext(p.Ctx, load)
			if err != nil {
				t.Fatal(err)
			}
			defer activate.Close()
			heldSet := fixture.HeldStatement{Executor: c, Statement: set, SQL: assignment}
			heldLoad := fixture.HeldStatement{Executor: c, Statement: activate, SQL: load}
			// Preparing either utility neither publishes the assignment nor
			// activates definitions, and cannot capture the preparer's authority.
			run(fixture.Select("SELECT current_setting('auto_explain.log_analyze', true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			run(fixture.Select("SELECT current_setting('auto_explain.log_format', true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			autoExplainQuiet(t, notices)
			run(fixture.Exec("SET ROLE ae_held_reader", 0))
			fixture.RunStep(t, p.Ctx, heldSet, b.Recorder, fixture.Exec(assignment, 0))
			run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"true"}}))
			denied := fixture.ErrorDiagnostic(load, fixture.ControlsReferenceDiagnostic("42501", `access to library "auto_explain" is not allowed`))
			denied.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, heldLoad, b.Recorder, denied)
			run(fixture.Select("SELECT current_setting('auto_explain.log_format', true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			autoExplainQuiet(t, notices)
			run(fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, heldLoad, b.Recorder, fixture.Exec(load, 0))
			autoExplainPromotionWarning(t, notices, "42501", `permission denied to set parameter "auto_explain.log_analyze"`, "")
			run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"off"}}))
			run(fixture.Exec("SET ROLE ae_held_reader", 0))
			denied = fixture.ErrorDiagnostic(assignment, fixture.ControlsReferenceDiagnostic("42501", `permission denied to set parameter "auto_explain.log_analyze"`))
			denied.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, heldSet, b.Recorder, denied)
			run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"off"}}))
			run(fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, heldLoad, b.Recorder, fixture.Exec(load, 0))
			autoExplainQuiet(t, notices)
		})
	})
}
