package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsWrappers(t *testing.T) {
	for _, call := range xml2SetCalls() {
		if call.name == "nodes4" || call.name == "list3" {
			continue // These are the kernels, not SQL wrappers.
		}
		t.Run(call.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"xml2_sets_reader", "xml2_sets_aggregate_reader"})
			xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				xml2Install(t, p, b, c)
				run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
				q := "SELECT " + call.expression + " AS value"
				bad := append([]any(nil), call.args...)
				bad[0], bad[1] = "<r/>", "["
				compile := xml2SetsSQLContext(xml2Compile("Invalid expression"), xml2SetsWrapperName(call))
				literal := "SELECT " + call.expression + " AS value"
				for i, v := range bad {
					literal = strings.ReplaceAll(literal, "$"+string(rune('1'+i))+"::text", "'"+v.(string)+"'::text")
				}
				for _, noninline := range []bool{false, true} {
					if noninline {
						run("ALTER FUNCTION " + call.signature + " SET search_path = public")
					}
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("parameterized wrapper preparation", err)
					}
					xml2SetsNotices(t, notices)
					var value any
					err = c.QueryRowContext(p.Ctx, literal).Scan(&value)
					fixture.AssertDiagnostic(t, err, &compile)
					fresh, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					err = fresh.QueryRowContext(p.Ctx, bad...).Scan(&value)
					fixture.AssertDiagnostic(t, err, &compile)
					fresh.Close()
					err = stmt.QueryRowContext(p.Ctx, bad...).Scan(&value)
					fixture.AssertDiagnostic(t, err, &compile)
					h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
					stmt.Close()
					if noninline {
						run("ALTER FUNCTION " + call.signature + " RENAME TO xml2_sets_outer")
						name := strings.Split(call.signature, "(")[0]
						currentQ := strings.Replace(q, name, "xml2_sets_outer", 1)
						err = c.QueryRowContext(p.Ctx, currentQ, bad...).Scan(&value)
						currentCompile := xml2SetsSQLContext(xml2Compile("Invalid expression"), "xml2_sets_outer")
						fixture.AssertDiagnostic(t, err, &currentCompile)
						currentStmt, err := c.PrepareContext(p.Ctx, currentQ)
						if err != nil {
							t.Fatal(err)
						}
						err = currentStmt.QueryRowContext(p.Ctx, bad...).Scan(&value)
						fixture.AssertDiagnostic(t, err, &currentCompile)
						currentHeld := fixture.HeldStatement{Executor: c, Statement: currentStmt, SQL: currentQ}
						fixture.RunStep(t, p.Ctx, currentHeld, b.Recorder, xml2SetValue(currentQ, call.healthy, call.args...))
						currentStmt.Close()
						err = c.QueryRowContext(p.Ctx, strings.Replace(literal, name, "xml2_sets_outer", 1)).Scan(&value)
						fixture.AssertDiagnostic(t, err, &currentCompile)
						currentSig := strings.Replace(call.signature, name, "xml2_sets_outer", 1)
						run("ALTER FUNCTION " + currentSig + " RENAME TO " + name)
						run("ALTER FUNCTION " + call.signature + " RESET ALL")
					}
				}
				xml2SetsAggregate(t, p, b, c, call, notices)
				xml2SetDependencies(t, p, b, c, "xpath_nodeset", "xpath_list")
				xml2SetsWrapperACL(t, p, b, c, call, notices)
			})
		})
	}
}

func xml2SetsWrapperACL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, call xml2SetCall, notices *[]fixture.Diagnostic) {
	t.Helper()
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	name, innerSig := "xpath_nodeset", "xpath_nodeset(text,text,text,text)"
	if call.name == "list2" {
		name, innerSig = "xpath_list", "xpath_list(text,text,text)"
	}
	innerDenied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function "+name), name)
	runStep := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE ROLE xml2_sets_reader", "GRANT USAGE ON SCHEMA public TO xml2_sets_reader",
		"GRANT EXECUTE ON FUNCTION " + call.signature + " TO xml2_sets_reader",
	} {
		run(q)
	}
	q := "SELECT " + call.expression + " AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	healthy := xml2SetValue(q, call.healthy, call.args...)
	run("SET ROLE xml2_sets_reader")
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	run("RESET ROLE")
	run("REVOKE EXECUTE ON FUNCTION " + innerSig + " FROM PUBLIC")
	for _, noninline := range []bool{false, true} {
		if noninline {
			run("ALTER FUNCTION " + call.signature + " SET search_path = public")
		}
		run("SET ROLE xml2_sets_reader")
		var value any
		err = stmt.QueryRowContext(p.Ctx, call.args...).Scan(&value)
		fixture.AssertDiagnostic(t, err, &innerDenied)
		// STRICT and dead CASE bypass the inner body. A live constant is
		// still demanded while planning a query with WHERE false.
		null := append([]any(nil), call.args...)
		null[0] = nil
		fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, nil, null...))
		runStep(xml2SetsQueryError(q, innerDenied, call.args...))
		for _, suffix := range []string{"", " WHERE false"} {
			expr := "xpath_nodeset(NULL::text,'/r/a')"
			if call.name == "nodes3" {
				expr = "xpath_nodeset(NULL::text,'/r/a','i')"
			} else if call.name == "list2" {
				expr = "xpath_list(NULL::text,'/r/a')"
			}
			s := xml2SetValue("SELECT "+expr+" AS value"+suffix, nil)
			if suffix != "" {
				s.Rows = nil
			}
			runStep(s)
			live := strings.Replace(expr, "NULL::text", "'<r><a>A</a></r>'", 1)
			runStep(xml2SetsQueryError("SELECT "+live+" AS value"+suffix, innerDenied))
			s = xml2SetValue("SELECT CASE WHEN false THEN "+live+" ELSE NULL END AS value"+suffix, nil)
			if suffix != "" {
				s.Rows = nil
			}
			runStep(s)
		}
		run("RESET ROLE")
		if noninline {
			run("ALTER FUNCTION " + call.signature + " RESET ALL")
		}
	}
	run("GRANT EXECUTE ON FUNCTION " + innerSig + " TO xml2_sets_reader")
	run("SET ROLE xml2_sets_reader")
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	run("RESET ROLE")
	// Outer and inner permissions are independent; the outer STRICT and
	// dead-CASE paths also bypass demand before entry checks.
	run("REVOKE EXECUTE ON FUNCTION " + call.signature + " FROM PUBLIC,xml2_sets_reader")
	run("SET ROLE xml2_sets_reader")
	var value any
	err = stmt.QueryRowContext(p.Ctx, call.args...).Scan(&value)
	outerDenied := xml2Error("42501", "permission denied for function "+name)
	fixture.AssertDiagnostic(t, err, &outerDenied)
	null := append([]any(nil), call.args...)
	null[0] = nil
	fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, nil, null...))
	runStep(xml2SetsQueryError(q+" WHERE false", outerDenied, call.args...))
	runStep(xml2SetValue("SELECT CASE WHEN false THEN "+call.expression+" ELSE NULL END AS value", nil, call.args...))
	literalNull := strings.ReplaceAll(q, "$1::text", "NULL::text")
	for i := 1; i < len(call.args); i++ {
		literalNull = strings.ReplaceAll(literalNull, "$"+string(rune('1'+i))+"::text", "'"+call.args[i].(string)+"'::text")
	}
	runStep(xml2SetValue(literalNull, nil))
	run("RESET ROLE")
	run("GRANT EXECUTE ON FUNCTION " + call.signature + " TO PUBLIC,xml2_sets_reader")
	run("SET ROLE xml2_sets_reader")
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	run("RESET ROLE")
	run("ALTER FUNCTION " + innerSig + " RENAME TO xml2_sets_inner")
	currentSig := strings.Replace(innerSig, name, "xml2_sets_inner", 1)
	run("ALTER EXTENSION xml2 DROP FUNCTION " + currentSig)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	xml2Both(t, p, b, c, healthy)
	nodes, list := "xpath_nodeset", "xpath_list"
	if name == "xpath_nodeset" {
		nodes = "xml2_sets_inner"
	} else {
		list = "xml2_sets_inner"
	}
	xml2SetDependencies(t, p, b, c, nodes, list)
	run("REVOKE EXECUTE ON FUNCTION " + currentSig + " FROM xml2_sets_reader")
	run("SET ROLE xml2_sets_reader")
	err = stmt.QueryRowContext(p.Ctx, call.args...).Scan(&value)
	currentDenied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function xml2_sets_inner"), name)
	fixture.AssertDiagnostic(t, err, &currentDenied)
	run("RESET ROLE")
	run("GRANT EXECUTE ON FUNCTION " + currentSig + " TO xml2_sets_reader")
	run("SET ROLE xml2_sets_reader")
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	run("RESET ROLE")
	_, err = c.ExecContext(p.Ctx, "DROP FUNCTION "+currentSig+" RESTRICT")
	restrict := xml2Error("2BP01", "cannot drop function "+currentSig+" because other objects depend on it")
	restrict.Detail = "extension xml2 depends on function " + currentSig
	restrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	fixture.AssertDiagnostic(t, err, &restrict)
	xml2SetsNotices(t, notices)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
	run("DROP FUNCTION " + currentSig + " CASCADE")
	xml2SetsNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to extension xml2"})
}

func xml2SetsAggregate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, call xml2SetCall, notices *[]fixture.Diagnostic) {
	t.Helper()
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	name, inner := "xpath_nodeset", "xpath_nodeset(text,text,text,text)"
	types, operands := "text", "$1::text"
	args := []any{"'<r><a>A</a></r>'"}
	if call.name == "nodes3" {
		types, operands = "text,text", "$1::text,$2::text"
		args = append(args, "i")
	} else if call.name == "list2" {
		name, inner = "xpath_list", "xpath_list(text,text,text)"
	}
	run("SET client_min_messages TO notice")
	run("CREATE ROLE xml2_sets_aggregate_reader")
	run("GRANT USAGE ON SCHEMA public TO xml2_sets_aggregate_reader")
	run("CREATE AGGREGATE xml2_sets_fold("+types+") (SFUNC="+name+",STYPE=text,INITCOND='<r/>',COMBINEFUNC="+name+",PARALLEL=SAFE)")
	defer func() {
		run("RESET ROLE")
		run("GRANT EXECUTE ON FUNCTION "+call.signature+" TO PUBLIC")
		run("GRANT EXECUTE ON FUNCTION "+inner+" TO PUBLIC")
		run("ALTER FUNCTION "+call.signature+" RESET ALL")
		run("DROP AGGREGATE xml2_sets_fold("+types+")")
	}()
	// Support EXECUTE belongs to the aggregate owner. The selected SQL body
	// still invokes its genuine inner kernel under the actual caller.
	run("REVOKE EXECUTE ON FUNCTION "+call.signature+" FROM PUBLIC")
	q := "SELECT xml2_sets_fold(" + operands + ") AS value FROM (VALUES(1),(2)) AS s(id)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	caller := fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}})
	for _, configured := range []bool{false, true} {
		if configured {
			run("ALTER FUNCTION "+call.signature+" SET client_min_messages = warning")
		}
		run("SET ROLE xml2_sets_aggregate_reader")
		outer := xml2Error("42501", "permission denied for function "+name)
		xml2Both(t, p, b, c, xml2SetsQueryError("SELECT "+call.expression+" AS value", outer, call.args...))
		xml2Both(t, p, b, c, xml2SetValue(q, "<r><a>A</a></r>", args...))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, xml2SetValue(q, "<r><a>A</a></r>", args...))
		bad := append([]any(nil), args...)
		bad[0] = "["
		compile := xml2SetsSQLContext(xml2Compile("Invalid expression"), name)
		err = stmt.QueryRowContext(p.Ctx, bad...).Scan(new(any))
		fixture.AssertDiagnostic(t, err, &compile)
		xml2SetsNotices(t, notices)
		xml2Both(t, p, b, c, caller)
		unsupported := append([]any(nil), args...)
		unsupported[0] = "true()"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, xml2SetValue(q, "<unsupported/>", unsupported...))
		if configured {
			xml2SetsNotices(t, notices)
		} else {
			notice := xml2SetsUnsupportedDiagnostic(false, name)
			xml2SetsNotices(t, notices, notice, notice)
		}
		xml2Both(t, p, b, c, caller)
		run("RESET ROLE")
		run("REVOKE EXECUTE ON FUNCTION "+inner+" FROM PUBLIC")
		run("SET ROLE xml2_sets_aggregate_reader")
		denied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function "+name), name)
		err = stmt.QueryRowContext(p.Ctx, args...).Scan(new(any))
		fixture.AssertDiagnostic(t, err, &denied)
		quiet := append([]any(nil), args...)
		quiet[0] = nil
		fixture.RunStep(t, p.Ctx, held, b.Recorder, xml2SetValue(q, "<r/>", quiet...))
		xml2SetsNotices(t, notices)
		xml2Both(t, p, b, c, caller)
		run("RESET ROLE")
		run("GRANT EXECUTE ON FUNCTION "+inner+" TO PUBLIC")
		run("SET ROLE xml2_sets_aggregate_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, xml2SetValue(q, "<r><a>A</a></r>", args...))
		run("RESET ROLE")
	}
}
