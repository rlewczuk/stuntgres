package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLXPathRuntimeWrappers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(t *testing.T, executor fixture.Executor, s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
			}
			run(t, c, fixture.Exec("CREATE SEQUENCE xpath_runtime_seq", 0))
			run(t, c, fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
			state := func(t *testing.T, n int64, called bool) {
				t.Helper()
				run(t, c, fixture.Select("SELECT last_value,is_called FROM xpath_runtime_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
			}
			bad := fixture.ControlsReferenceDiagnostic("10608", "invalid XPath expression")
			bad.Detail = "Invalid expression"
			failure := func(q string, args ...any) fixture.Step {
				s := fixture.ErrorDiagnostic(q, bad, args...)
				s.SelectRows = true
				s.ErrorStage = "execute"
				return s
			}
			for _, name := range []string{"xpath", "xpath_exists"} {
				for _, overload := range []struct{ label, mapping string }{
					{"two_arguments", ""},
					{"three_arguments", ",'{}'::text[]"},
				} {
					for _, operand := range []struct {
						label, sql string
						volatile   bool
					}{
						{"stable", "current_setting('application_name')", false},
						{"volatile", "nextval('xpath_runtime_seq')::text", true},
					} {
						for _, route := range []string{"direct", "fresh_prepared", "held", "held_reuse"} {
							t.Run(name+"/"+overload.label+"/"+operand.label+"/"+route, func(t *testing.T) {
								run(t, c, fixture.Exec("ALTER SEQUENCE xpath_runtime_seq RESTART WITH 1", 0))
								run(t, c, fixture.Exec("SET application_name='1'", 0))
								state(t, 1, false)
								q := "SELECT " + name + "('[' || " + operand.sql + ",'<r/>'::xml" + overload.mapping + ") AS v"
								if route == "held_reuse" {
									q = "SELECT " + name + "($1::text || " + operand.sql + " || $2::text,'<r><a/><a/><a/><a/><a/></r>'::xml" + overload.mapping + ") AS v"
								}
								var executor fixture.Executor = c
								if route == "held" || route == "held_reuse" {
									stmt, err := c.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatalf("PREPARE must succeed before XPath execution: %v", err)
									}
									defer stmt.Close()
									executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
									// PREPARE must neither consume nextval nor evaluate bad XPath.
									state(t, 1, false)
								}
								if route != "held_reuse" {
									s := failure(q)
									s.Prepared = route == "fresh_prepared"
									run(t, executor, s)
									state(t, 1, operand.volatile)
									return
								}
								for i, tc := range []struct {
									prefix, suffix string
									invalid, match bool
								}{
									{"[", "", true, false},
									{"/r/a[", "]", false, true},
									{"/missing[", "]", false, false},
									{"[", "", true, false},
									{"/r/a[", "]", false, true},
								} {
									if !operand.volatile {
										run(t, c, fixture.Exec("SET application_name='"+strconv.Itoa(i+1)+"'", 0))
									}
									if tc.invalid {
										run(t, executor, failure(q, tc.prefix, tc.suffix))
									} else {
										typ, want := "_XML", any("{}")
										if tc.match {
											want = "{<a/>}"
										}
										if name == "xpath_exists" {
											typ, want = "BOOL", tc.match
										}
										s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, tc.prefix, tc.suffix)
										s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
											cols, err := rows.ColumnTypes()
											if err != nil || len(cols) != 1 {
												t.Fatal(cols, err)
											}
											col := cols[0]
											scan := reflect.TypeFor[bool]()
											if typ == "_XML" {
												scan = reflect.TypeFor[string]()
												if b.PG {
													scan = reflect.TypeFor[any]()
												}
											}
											length, lk := col.Length()
											precision, scale, dk := col.DecimalSize()
											nullable, nk := col.Nullable()
											if col.Name() != "v" || col.DatabaseTypeName() != typ || col.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
												t.Fatalf("computed %s descriptor: name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", typ, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lk, precision, scale, dk, nullable, nk)
											}
										}
										run(t, executor, s)
									}
									if operand.volatile {
										state(t, int64(i+1), true)
									} else {
										state(t, 1, false)
									}
								}
							})
						}
					}
					for _, barrier := range []struct {
						label, path, document, heldPath, heldDocument string
						badPath, badDocument, childCode, childMessage string
						documentInput                                 bool
					}{
						{
							"xmlparse_document", "'['", "XMLPARSE(DOCUMENT '<r/>')", "$1::text", "XMLPARSE(DOCUMENT $2::text)",
							"'['", "XMLPARSE(DOCUMENT '<a>')", "2200M", "invalid XML document", true,
						},
						{
							"xmlserialize_path", "XMLSERIALIZE(CONTENT XMLPARSE(CONTENT '[') AS text)", "'<r/>'::xml",
							"XMLSERIALIZE(CONTENT XMLPARSE(CONTENT $1::text) AS text)", "$2::xml",
							"XMLSERIALIZE(CONTENT XMLPARSE(CONTENT '<a>') AS text)", "'<r/>'::xml", "2200N", "invalid XML content", false,
						},
					} {
						for _, route := range []string{"direct", "fresh_prepared", "held", "held_reuse"} {
							t.Run(name+"/"+overload.label+"/"+barrier.label+"/"+route, func(t *testing.T) {
								child := fixture.ControlsReferenceDiagnostic(barrier.childCode, barrier.childMessage)
								child.Detail = "line 1: Premature end of data in tag a line 1\n<a>\n   ^"
								query := func(path, document string) string {
									return "SELECT " + name + "(" + path + "," + document + overload.mapping + ") AS v"
								}
								if route != "held_reuse" {
									for _, tc := range []struct {
										path, document string
										diagnostic     fixture.Diagnostic
									}{
										{barrier.path, barrier.document, bad},
										{barrier.badPath, barrier.badDocument, child},
									} {
										q := query(tc.path, tc.document)
										s := fixture.ErrorDiagnostic(q, tc.diagnostic)
										s.SelectRows, s.ErrorStage = true, "execute"
										s.Prepared = route == "fresh_prepared"
										var executor fixture.Executor = c
										if route == "held" {
											stmt, err := c.PrepareContext(p.Ctx, q)
											if err != nil {
												t.Fatalf("PREPARE must not execute XML expressions: %v", err)
											}
											defer stmt.Close()
											executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
										}
										run(t, executor, s)
									}
									return
								}
								q := query(barrier.heldPath, barrier.heldDocument)
								stmt, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
								const document = "<r><a/><a/><a/><a/><a/></r>"
								for _, tc := range []struct {
									path, document string
									diagnostic     *fixture.Diagnostic
									match          bool
								}{
									{"[", document, &bad, false},
									{"/r/a[2]", document, nil, true},
									{"/missing", document, nil, false},
									{"<a>", "<a>", &child, false},
									{"/r/a[5]", document, nil, true},
								} {
									args := []any{tc.path, tc.document}
									if tc.diagnostic == &child {
										if barrier.documentInput {
											args[0] = "["
										} else {
											args[1] = document
										}
									}
									if tc.diagnostic != nil {
										s := fixture.ErrorDiagnostic(q, *tc.diagnostic, args...)
										s.SelectRows, s.ErrorStage = true, "execute"
										run(t, held, s)
									} else {
										typ, want := "_XML", any("{}")
										if tc.match {
											want = "{<a/>}"
										}
										if name == "xpath_exists" {
											typ, want = "BOOL", tc.match
										}
										run(t, held, fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...))
									}
								}
							})
						}
					}
				}
			}
		})
	}
}
