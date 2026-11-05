package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsDiagnostics(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		for _, call := range xml2SetCalls() {
			t.Run(call.name, func(t *testing.T) {
				q := "SELECT " + call.expression + " AS value"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("quiet parameterized preparation", err)
				}
				defer stmt.Close()
				xml2SetsNotices(t, notices)
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, doc := range []any{"", []byte{}, "<r>", "<r>&missing;</r>", "<r a='1' a='2'/>", "<r/>extra"} {
					args := append([]any(nil), call.args...)
					args[0], args[1] = doc, "["
					xml2Both(t, p, b, c, xml2SetValue(q, nil, args...))
					fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, nil, args...))
				}
				for _, path := range []string{"$v", "unknown()", "/p:r"} {
					args := append([]any(nil), call.args...)
					args[0], args[1] = "<r/>", path
					xml2Both(t, p, b, c, xml2SetValue(q, nil, args...))
				}
				for i := range call.args {
					for _, null := range []any{nil, []byte(nil)} {
						args := append([]any(nil), call.args...)
						args[0], args[1] = "<r>", "["
						args[i] = null
						xml2Both(t, p, b, c, xml2SetValue(q, nil, args...))
						fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, nil, args...))
					}
				}
				// Nonnil empty bytes are empty TEXT on both client routes, not NULL.
				args := append([]any(nil), call.args...)
				args[0] = []byte("<r><a/></r>")
				args[1] = []byte("/r/a")
				want := "<a/>"
				switch call.name {
				case "nodes3":
					want = "<i><a/></i>"
				case "nodes4":
					want = "<o><i><a/></i></o>"
				case "list2", "list3":
					want = ""
				}
				xml2Both(t, p, b, c, xml2SetValue(q, want, args...))
				for i := 2; i < len(call.args); i++ {
					empty := append([]any(nil), call.args...)
					empty[i] = []byte{}
					want := call.healthy
					switch call.name {
					case "nodes3":
						want = xml2SetsNodes
					case "nodes4":
						if i == 2 {
							want = xml2SetsItems
						} else {
							want = "<o>" + xml2SetsNodes + "</o>"
						}
					case "list3":
						want = "ABCD&E"
					}
					xml2Both(t, p, b, c, xml2SetValue(q, want, empty...))
					fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, want, empty...))
				}
				for _, path := range []any{"[", "", []byte{}, " ", "\t\n"} {
					bad := append([]any(nil), call.args...)
					bad[0], bad[1] = "<r/>", path
					for _, held := range []bool{false, true} {
						var value any
						if held {
							err = stmt.QueryRowContext(p.Ctx, bad...).Scan(&value)
						} else {
							err = c.QueryRowContext(p.Ctx, q, bad...).Scan(&value)
						}
						d := xml2SetsSQLContext(xml2Compile("Invalid expression"), xml2SetsWrapperName(call))
						fixture.AssertDiagnostic(t, err, &d)
						fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetValue(q, call.healthy, call.args...))
					}
				}
				xml2SetsNotices(t, notices)
			})
		}
		for _, tc := range xml2LegacyDiagnostics {
			detail := "line 1: " + tc.message + "\n" + tc.doc + "\n" + strings.Repeat(" ", tc.caret) + "^\nInvalid expression"
			for _, expression := range []string{"xpath_nodeset($1,$2,'','')", "xpath_list($1,$2,'|')"} {
				xml2Failure(t, p, b, c, "SELECT "+expression, xml2Compile(detail), false, tc.doc, "[")
				xml2Both(t, p, b, c, xml2SetValue("SELECT "+expression+" AS value", nil, tc.doc, "$v"))
			}
		}
		for _, tc := range []struct{ doc, path, nodes, list string }{
			{`<!DOCTYPE r [<!ENTITY e "<a>X</a>">]><r>&e;</r>`, "/r/a", "<a>X</a>", "X"},
			{`<!DOCTYPE r [<!ATTLIST r a CDATA "default">]><r/>`, "/r/@a", "", ""},
			{`<?xml version="1.0" encoding="ISO-8859-1"?><r>é</r>`, "/r/text()", "Ã©", "Ã©"},
		} {
			xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2,'','') AS value", tc.nodes, tc.doc, tc.path))
			xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_list($1,$2,'|') AS value", tc.list, tc.doc, tc.path))
		}
	})
}
