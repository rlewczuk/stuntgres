package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Force each planner contract explicitly. PostgreSQL's automatic custom/generic
// cost transition is not an execution-count contract for the native engine.
func TestReferenceXML2SetsPlanning(t *testing.T) {
	for _, call := range xml2SetCalls() {
		if xml2SetsWrapperName(call) == "" {
			continue
		}
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			for _, config := range []string{"inline_eligible", "search_path"} {
				t.Run(call.name+"/"+mode+"/"+config, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"xml2_sets_plan_reader"})
					xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
						xml2SetsPlanningCase(t, p, b, c, notices, call, mode, config == "search_path")
					})
				})
			}
		}
	}
}

func xml2SetsPlanningCase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic, call xml2SetCall, mode string, configured bool) {
	t.Helper()
	xml2Install(t, p, b, c)
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	run("SET plan_cache_mode = " + mode)
	if configured {
		run("ALTER FUNCTION " + call.signature + " SET search_path = public")
	}
	name := xml2SetsWrapperName(call)
	contextName := name
	inlined := mode == "force_generic_plan" && !configured
	if inlined {
		contextName = ""
	}
	q := "SELECT " + call.expression + " AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("quiet parameterized preparation", err)
	}
	defer stmt.Close()
	xml2SetsNotices(t, notices)
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	// Separate assertion leaves keep every required route observable if an
	// independently specified expectation is disproved by the release oracle.
	check := func(label string, x fixture.Executor, s fixture.Step, want ...fixture.Diagnostic) {
		t.Helper()
		t.Run(label, func(t *testing.T) {
			defer xml2SetsNotices(t, notices, want...)
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		})
	}
	held := func(label string, s fixture.Step, want ...fixture.Diagnostic) {
		t.Helper()
		check(label, h, s, want...)
	}
	bad := append([]any(nil), call.args...)
	bad[0], bad[1] = "<r/>", "["
	compile := xml2SetsSQLContext(xml2Compile("Invalid expression"), contextName)
	for _, route := range []string{"direct", "prepared", "held"} {
		x := fixture.Executor(c)
		s := xml2SetsQueryError(q, compile, bad...)
		if route == "prepared" {
			s.Prepared = true
		} else if route == "held" {
			x = h
		}
		check("compile_"+route, x, s)
	}
	for _, path := range []string{"count(/r/a)", "true()"} {
		args := append([]any(nil), call.args...)
		args[1] = path
		for _, route := range []string{"direct", "prepared", "held"} {
			x := fixture.Executor(c)
			s := xml2SetValue(q, "<unsupported/>", args...)
			if route == "prepared" {
				s.Prepared = true
			} else if route == "held" {
				x = h
			}
			check("notice_"+path+"_"+route, x, s, xml2SetsUnsupportedDiagnostic(path == "count(/r/a)", contextName))
		}
	}
	healthy := xml2SetValue(q, call.healthy, call.args...)
	held("healthy_after_errors", healthy)
	null := append([]any(nil), call.args...)
	null[0] = nil
	held("null_allowed", xml2SetValue(q, nil, null...))
	literalNull := strings.ReplaceAll(q, "$1::text", "NULL::text")
	for i := 1; i < len(call.args); i++ {
		literalNull = strings.ReplaceAll(literalNull, "$"+string(rune('1'+i))+"::text", "'"+call.args[i].(string)+"'::text")
	}
	innerSig := "xpath_nodeset(text,text,text,text)"
	if name == "xpath_list" {
		innerSig = "xpath_list(text,text,text)"
	}
	for _, sql := range []string{
		"CREATE ROLE xml2_sets_plan_reader", "GRANT USAGE ON SCHEMA public TO xml2_sets_plan_reader",
		"GRANT EXECUTE ON FUNCTION " + call.signature + " TO xml2_sets_plan_reader",
		"REVOKE EXECUTE ON FUNCTION " + innerSig + " FROM PUBLIC",
		"SET ROLE xml2_sets_plan_reader",
	} {
		run(sql)
	}
	innerDenied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function "+name), contextName)
	held("inner_denied", xml2SetsQueryError(q, innerDenied, call.args...))
	if inlined {
		// Generic inlining exposes the kernel's runtime entry check.
		held("inner_denied_parameter_null", xml2SetsQueryError(q, innerDenied, null...))
	} else {
		held("inner_denied_parameter_null", xml2SetValue(q, nil, null...))
	}
	check("inner_denied_literal_null", c, xml2SetValue(literalNull, nil))
	run("RESET ROLE")
	run("GRANT EXECUTE ON FUNCTION " + innerSig + " TO xml2_sets_plan_reader")
	run("SET ROLE xml2_sets_plan_reader")
	held("inner_regrant", healthy)
	run("RESET ROLE")
	run("REVOKE EXECUTE ON FUNCTION " + call.signature + " FROM PUBLIC,xml2_sets_plan_reader")
	run("SET ROLE xml2_sets_plan_reader")
	outerDenied := xml2Error("42501", "permission denied for function "+name)
	held("outer_denied", xml2SetsQueryError(q, outerDenied, call.args...))
	if mode == "force_generic_plan" {
		held("outer_denied_parameter_null", xml2SetsQueryError(q, outerDenied, null...))
	} else {
		held("outer_denied_parameter_null", xml2SetValue(q, nil, null...))
	}
	check("outer_denied_literal_null", c, xml2SetValue(literalNull, nil))
	check("outer_denied_dead_case", c, xml2SetValue("SELECT CASE WHEN false THEN "+call.expression+" ELSE NULL END AS value", nil, call.args...))
	run("RESET ROLE")
	run("GRANT EXECUTE ON FUNCTION " + call.signature + " TO PUBLIC,xml2_sets_plan_reader")
	run("SET ROLE xml2_sets_plan_reader")
	held("outer_regrant", healthy)
	run("RESET ROLE")
	run("ALTER FUNCTION " + innerSig + " RENAME TO xml2_sets_plan_inner")
	currentSig := strings.Replace(innerSig, name, "xml2_sets_plan_inner", 1)
	run("ALTER EXTENSION xml2 DROP FUNCTION " + currentSig)
	held("renamed_detached_inner", healthy)
	held("renamed_inner_compile", xml2SetsQueryError(q, compile, bad...))
	run("REVOKE EXECUTE ON FUNCTION " + currentSig + " FROM xml2_sets_plan_reader")
	run("SET ROLE xml2_sets_plan_reader")
	currentDenied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function xml2_sets_plan_inner"), contextName)
	held("renamed_inner_denied", xml2SetsQueryError(q, currentDenied, call.args...))
	if inlined {
		held("renamed_inner_denied_parameter_null", xml2SetsQueryError(q, currentDenied, null...))
	} else {
		held("renamed_inner_denied_parameter_null", xml2SetValue(q, nil, null...))
	}
	run("RESET ROLE")
	run("GRANT EXECUTE ON FUNCTION " + currentSig + " TO xml2_sets_plan_reader")
	run("SET ROLE xml2_sets_plan_reader")
	held("renamed_inner_regrant", healthy)
	run("RESET ROLE")
	if configured {
		run("ALTER FUNCTION " + call.signature + " RENAME TO xml2_sets_plan_outer")
		outerQ := strings.Replace(q, name, "xml2_sets_plan_outer", 1)
		currentCompile := xml2SetsSQLContext(xml2Compile("Invalid expression"), "xml2_sets_plan_outer")
		check("renamed_outer_direct", c, xml2SetsQueryError(outerQ, currentCompile, bad...))
		s := xml2SetsQueryError(outerQ, currentCompile, bad...)
		s.Prepared = true
		check("renamed_outer_prepared", c, s)
		outerStmt, err := c.PrepareContext(p.Ctx, outerQ)
		if err != nil {
			t.Fatal(err)
		}
		defer outerStmt.Close()
		xml2SetsNotices(t, notices)
		outerHeld := fixture.HeldStatement{Executor: c, Statement: outerStmt, SQL: outerQ}
		check("renamed_outer_held", outerHeld, xml2SetsQueryError(outerQ, currentCompile, bad...))
		check("renamed_outer_healthy", outerHeld, xml2SetValue(outerQ, call.healthy, call.args...))
		outerSig := strings.Replace(call.signature, name, "xml2_sets_plan_outer", 1)
		run("ALTER FUNCTION " + outerSig + " RENAME TO " + name)
		run("ALTER FUNCTION " + call.signature + " RESET ALL")
	}
	xml2SetsNotices(t, notices)
}
