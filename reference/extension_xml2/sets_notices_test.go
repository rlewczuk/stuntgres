package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2SetsUnsupportedDiagnostic(number bool, routine string) fixture.Diagnostic {
	kind := "2"
	if number {
		kind = "3"
	}
	return xml2SetsSQLContext(fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "unsupported XQuery result: " + kind}, routine)
}

func xml2SetsUnsupported(t *testing.T, notices *[]fixture.Diagnostic, call xml2SetCall, number bool) {
	t.Helper()
	xml2SetsNotices(t, notices, xml2SetsUnsupportedDiagnostic(number, xml2SetsWrapperName(call)))
}

func TestReferenceXML2SetsNotices(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		for _, call := range xml2SetCalls() {
			t.Run(call.name, func(t *testing.T) {
				q := "SELECT " + call.expression + " AS value"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				xml2SetsNotices(t, notices)
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				bad := append([]any(nil), call.args...)
				bad[0], bad[1] = "<r/>", "["
				var value any
				err = stmt.QueryRowContext(p.Ctx, bad...).Scan(&value)
				d := xml2SetsSQLContext(xml2Compile("Invalid expression"), xml2SetsWrapperName(call))
				fixture.AssertDiagnostic(t, err, &d)
				xml2SetsNotices(t, notices)
				for _, path := range []string{"count(/r/a)", "true()"} {
					args := append([]any(nil), call.args...)
					args[1] = path
					for _, route := range []string{"direct", "prepared", "held"} {
						s := xml2SetValue(q, "<unsupported/>", args...)
						x := fixture.Executor(c)
						if route == "prepared" {
							s.Prepared = true
						} else if route == "held" {
							x = h
						}
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
						xml2SetsUnsupported(t, notices, call, path == "count(/r/a)")
					}
				}
				for _, input := range [][2]any{{"<r>", "["}, {nil, "true()"}, {"<r/>", "$v"}} {
					args := append([]any(nil), call.args...)
					args[0], args[1] = input[0], input[1]
					fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, nil, args...))
					xml2SetsNotices(t, notices)
				}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
				xml2SetsNotices(t, notices)
				// Pinned preparation is quiet; execution emits one folding notice
				// even with WHERE false. A genuinely dead CASE emits none.
				expr := call.expression
				for i, literal := range []string{"'<r/>'", "'true()'", "'i'", "'i'"} {
					expr = strings.ReplaceAll(expr, "$"+string(rune('1'+i))+"::text", literal)
				}
				for _, suffix := range []string{"", " WHERE false"} {
					literalQ := "SELECT " + expr + " AS value" + suffix
					literal, err := c.PrepareContext(p.Ctx, literalQ)
					if err != nil {
						t.Fatal(err)
					}
					xml2SetsNotices(t, notices)
					literalHeld := fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalQ}
					s := xml2SetValue(literalQ, "<unsupported/>")
					if suffix != "" {
						s.Rows = nil
					}
					fixture.RunStep(t, p.Ctx, literalHeld, b.Recorder, s)
					xml2SetsUnsupported(t, notices, call, false)
					literal.Close()
				}
				xml2Both(t, p, b, c, xml2SetValue("SELECT CASE WHEN false THEN "+expr+" ELSE NULL END AS value", nil))
				xml2SetsNotices(t, notices)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
				xml2SetsNotices(t, notices)
				if call.name != "nodes4" && call.name != "list3" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+call.signature+" SET search_path = public", 0))
					for _, path := range []string{"true()", "count(/r/a)"} {
						args := append([]any(nil), call.args...)
						args[1] = path
						fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, "<unsupported/>", args...))
						xml2SetsUnsupported(t, notices, call, path == "count(/r/a)")
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+call.signature+" RESET ALL", 0))
					fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
					xml2SetsNotices(t, notices)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET client_min_messages TO notice", 0))
					caller := fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}})
					for _, threshold := range []string{"warning", "error"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+call.signature+" SET client_min_messages = "+threshold, 0))
						args := append([]any(nil), call.args...)
						args[1] = "count(/r/a)"
						for _, route := range []string{"direct", "prepared", "held"} {
							x := fixture.Executor(c)
							s := xml2SetValue(q, "<unsupported/>", args...)
							if route == "prepared" {
								s.Prepared = true
							} else if route == "held" {
								x = h
							}
							fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
							xml2SetsNotices(t, notices)
							xml2Both(t, p, b, c, caller)
						}
						err = stmt.QueryRowContext(p.Ctx, bad...).Scan(&value)
						fixture.AssertDiagnostic(t, err, &d)
						xml2SetsNotices(t, notices)
						xml2Both(t, p, b, c, caller)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+call.signature+" RESET client_min_messages", 0))
						fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, "<unsupported/>", args...))
						xml2SetsUnsupported(t, notices, call, true)
						xml2Both(t, p, b, c, caller)
						fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
						xml2SetsNotices(t, notices)
					}
				}
				for _, threshold := range []string{"notice", "warning", "error", "notice"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET client_min_messages TO "+threshold, 0))
					for _, route := range []string{"direct", "prepared", "held"} {
						args := append([]any(nil), call.args...)
						args[1] = "count(/r/a)"
						s := xml2SetValue(q, "<unsupported/>", args...)
						x := fixture.Executor(c)
						if route == "prepared" {
							s.Prepared = true
						} else if route == "held" {
							x = h
						}
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
						if threshold == "notice" {
							xml2SetsUnsupported(t, notices, call, true)
						} else {
							xml2SetsNotices(t, notices)
						}
					}
				}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
				xml2SetsNotices(t, notices)
			})
		}
	})
}
