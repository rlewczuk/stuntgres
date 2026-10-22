package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONTableReviewRegressions(t *testing.T) {
	scenario := func(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
		t.Helper()
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				body(t, p, b, c)
			})
		}
	}

	t.Run("operator parameter defaults", func(t *testing.T) {
		scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_review_default START 1`, 0))
			// Volatility keeps zero-divisor controls from PostgreSQL custom-plan
			// folding; only demanded DEFAULT branches may advance this sequence.
			q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v int PATH '$.v' DEFAULT (($2::int + 1) / ($4::int + nextval('jt_review_default')::int * 0)) ON EMPTY,e int PATH '$.e' DEFAULT (($3::int + 2) / ($4::int + nextval('jt_review_default')::int * 0)) ON ERROR))`
			for _, route := range []string{"direct", "prepared", "same held"} {
				t.Run(route, func(t *testing.T) {
					var executor fixture.Executor = c
					if route == "same held" {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					for _, tc := range []struct {
						name          string
						doc           any
						x, y, divisor int
						rows          [][]any
					}{
						{"selected", `{"e":"bad"}`, 7, 10, 1, [][]any{{int64(8), int64(12)}}},
						{"changed parameters", `{"e":"bad"}`, 9, 20, 1, [][]any{{int64(10), int64(22)}}},
						{"both unselected", `{"v":4,"e":6}`, 100, 200, 0, [][]any{{int64(4), int64(6)}}},
						{"json null unselected", `{"v":null,"e":null}`, 100, 200, 0, [][]any{{nil, nil}}},
						{"null document", nil, 100, 200, 0, nil},
						{"recovery", `{"e":"bad"}`, 11, 30, 1, [][]any{{int64(12), int64(32)}}},
					} {
						t.Run(tc.name, func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER SEQUENCE jt_review_default RESTART WITH 1`, 0))
							s := fixture.Select(q, []string{"v", "e"}, []string{"INT4", "INT4"}, tc.rows, tc.doc, tc.x, tc.y, tc.divisor)
							s.Prepared = route == "prepared"
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
							next := int64(1)
							if tc.name == "selected" || tc.name == "changed parameters" || tc.name == "recovery" {
								next = 3
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, sqljsonQueryOne(`SELECT nextval('jt_review_default') AS v`, "INT8", next))
						})
					}
				})
			}
		})
	})

	t.Run("sibling competing diagnostics", func(t *testing.T) {
		for _, tc := range []struct{ name, columns, doc, key string }{
			{"column versus later sibling", `NESTED '$.a[*]' COLUMNS(v int PATH '$' ERROR ON ERROR),NESTED 'strict $.missing' COLUMNS(w int PATH '$')`, `{"a":["bad"]}`, "missing"},
			{"grandchild versus immediate sibling", `NESTED '$.a[*]' COLUMNS(NESTED 'strict $.grandchild' COLUMNS(v int PATH '$')),NESTED 'strict $.missing' COLUMNS(w int PATH '$')`, `{"a":[{}]}`, "missing"},
			{"left sibling path first", `NESTED 'strict $.first' COLUMNS(v int PATH '$'),NESTED 'strict $.missing' COLUMNS(w int PATH '$')`, `{}`, "first"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(` + tc.columns + `) ERROR ON ERROR)`
					d := fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "`+tc.key+`"`)
					sqljsonDiagnostic(t, p, b, c, q, d, false, tc.doc)
				})
			})
		}
	})

	t.Run("sibling volatile effects", func(t *testing.T) {
		for _, passing := range []bool{false, true} {
			name := "default only"
			if passing {
				name = "passing and default"
			}
			t.Run(name, func(t *testing.T) {
				for _, route := range []string{"direct", "prepared", "same held"} {
					t.Run(route, func(t *testing.T) {
						scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							run(fixture.Exec(`CREATE SEQUENCE jt_review_effect START 1`, 0))
							bindings := ""
							if passing {
								bindings = ` PASSING nextval('jt_review_effect') AS p`
							}
							q := `SELECT * FROM JSON_TABLE($1::jsonb,'$'` + bindings + ` COLUMNS(NESTED '$.a[*]' COLUMNS(v bigint PATH '$.absent' DEFAULT nextval('jt_review_effect') ON EMPTY),NESTED 'strict $.missing[*]' COLUMNS(w int PATH '$')) ERROR ON ERROR)`
							var executor fixture.Executor = c
							if route == "same held" {
								stmt, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							}
							for _, phase := range []string{"error", "recovery", "empty first sibling", "error again"} {
								t.Run(phase, func(t *testing.T) {
									run(fixture.Exec(`ALTER SEQUENCE jt_review_effect RESTART WITH 1`, 0))
									var s fixture.Step
									next := int64(1)
									if passing {
										next = 2
									}
									switch phase {
									case "error", "error again":
										s = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "missing"`), `{"a":[{}]}`)
										s.SelectRows = true
										s.ErrorStage = "execute"
									case "recovery":
										v := int64(1)
										next = 2
										if passing {
											v, next = 3, 5
										}
										s = fixture.Select(q, []string{"v", "w"}, []string{"INT8", "INT4"}, [][]any{{v, nil}, {nil, int64(5)}}, `{"a":[{}],"missing":[5]}`)
										s.Bag = true
									case "empty first sibling":
										if passing {
											next = 3
										}
										s = fixture.Select(q, []string{"v", "w"}, []string{"INT8", "INT4"}, [][]any{{nil, int64(5)}}, `{"a":[],"missing":[5]}`)
									}
									s.Prepared = route == "prepared"
									// A row/diagnostic failure must not hide the independent effect witness.
									t.Run("result", func(t *testing.T) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) })
									t.Run("next sequence value", func(t *testing.T) {
										fixture.RunStep(t, p.Ctx, c, b.Recorder, sqljsonQueryOne(`SELECT nextval('jt_review_effect') AS v`, "INT8", next))
									})
								})
							}
						})
					})
				}
			})
		}
	})

	t.Run("implicit root namespace", func(t *testing.T) {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			for _, declaration := range []string{
				`json_table_path_0 int`,
				`NESTED '$.a' AS json_table_path_0 COLUMNS(v int)`,
			} {
				t.Run(prefix+declaration, func(t *testing.T) {
					scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						q := prefix + `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(` + declaration + `))`
						d := fixture.ControlsReferenceDiagnostic("42712", `duplicate JSON_TABLE column or path name: json_table_path_0`)
						d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "json_table_path_0")])) + 1)
						sqljsonDiagnostic(t, p, b, c, q, d, true)
					})
				})
			}
			t.Run(prefix+"explicit root permits identifier", func(t *testing.T) {
				scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := prefix + `SELECT * FROM JSON_TABLE('{"json_table_path_0":7}','$' AS explicit_root COLUMNS(json_table_path_0 int))`
					sqljsonBoth(t, p, b, c, fixture.Select(q, []string{"json_table_path_0"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			})
		}
	})
}

func TestReferenceJSONTableDefaultCastShapes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jt_review_int AS int`, 0))
			for _, tc := range []struct {
				name, typ, value, databaseType string
				arg, want                      any
				reject                         bool
			}{
				{"cast function", "bigint", "($1::int)::bigint", "INT8", 7, int64(7), false},
				{"IO", "text", "($1::int)::text", "", 7, nil, true},
				{"relabel", "text", "($1::text)::varchar", "", "abc", nil, true},
				{"typmod function", "varchar(3)", "($1::text)::varchar(3)", "VARCHAR", "abc", "abc", false},
				{"array element functions", "int[]", "($1::int[])::bigint[]", "_INT4", "{7}", "{7}", false},
				{"domain", "int", "($1::int)::jt_review_int", "", 7, nil, true},
			} {
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					t.Run(prefix+tc.name, func(t *testing.T) {
						q := prefix + `SELECT * FROM JSON_TABLE('{}', '$' COLUMNS(v ` + tc.typ + ` PATH '$.missing' DEFAULT ` + tc.value + ` ON EMPTY))`
						if tc.reject {
							d := fixture.ControlsReferenceDiagnostic("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT")
							d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "$1")])) + 1)
							sqljsonDiagnostic(t, p, b, c, q, d, true, tc.arg)
						} else {
							sqljsonBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{tc.databaseType}, [][]any{{tc.want}}, tc.arg))
						}
					})
				}
			}
		})
	}
}

func TestReferenceJSONTableDefaultRegclassConstant(t *testing.T) {
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_review_regclass`, 0))
		q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v text PATH '$.missing' DEFAULT ('jt_review_regclass'::regclass)::text ON EMPTY))`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ doc, want string }{
			{`{}`, "jt_review_regclass"},
			{`{"missing":"provided"}`, "provided"},
			{`{}`, "jt_review_regclass"},
		} {
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.doc)
			sqljsonBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		invalid := `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(v text DEFAULT ($1::regclass)::text ON EMPTY))`
		d := fixture.ControlsReferenceDiagnostic("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT")
		d.Position = fixture.Position(strings.Index(invalid, "$1") + 1)
		sqljsonDiagnostic(t, p, b, c, invalid, d, true, "jt_review_regclass")
	})
}

func TestReferenceJSONTableDefaultWrapperReferences(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_review_wrapper`, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jt_review_columns(x int)`, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jt_review_columns VALUES(7)`, 1))
			// Generated relation OIDs are backend-local, not cross-backend goldens.
			// Obtain a real supported sequence identity, then exercise the numeric
			// operator -> OID -> specialized regclass -> text route with it.
			var sequenceOID int64
			if err := c.QueryRowContext(p.Ctx, `SELECT 'jt_review_wrapper'::regclass::oid::bigint`).Scan(&sequenceOID); err != nil {
				t.Fatal(err)
			}
			if sequenceOID <= 0 || sequenceOID > 2147483647 {
				t.Fatalf("sequence OID %d cannot exercise the INT4 operator route", sequenceOID)
			}

			t.Run("operator under specialized regclass", func(t *testing.T) {
				q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v text PATH '$.missing' DEFAULT (($2::int + 0)::oid::regclass)::text ON EMPTY))`
				for _, route := range []string{"direct", "prepared", "same held"} {
					t.Run(route, func(t *testing.T) {
						var executor fixture.Executor = c
						if route == "same held" {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						// Pinned PostgreSQL18.6 independently confirms these results.
						for _, tc := range []struct {
							name string
							doc  any
							oid  int64
							rows [][]any
						}{
							{"selected zero OID", `{}`, 0, [][]any{{"-"}}},
							{"changed to real sequence OID", `{}`, sequenceOID, [][]any{{"jt_review_wrapper"}}},
							{"unselected zero OID", `{"missing":"provided"}`, 0, [][]any{{"provided"}}},
							{"unselected real OID", `{"missing":"changed"}`, sequenceOID, [][]any{{"changed"}}},
							{"JSON null unselected", `{"missing":null}`, 0, [][]any{{nil}}},
							{"SQL NULL root", nil, sequenceOID, nil},
							{"recovery zero OID", `{}`, 0, [][]any{{"-"}}},
							{"recovery real OID", `{}`, sequenceOID, [][]any{{"jt_review_wrapper"}}},
						} {
							t.Run(tc.name, func(t *testing.T) {
								s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, tc.rows, tc.doc, tc.oid)
								s.Prepared = route == "prepared"
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, jsonTableMetadata(s, b.PG))
							})
						}
					})
				}
			})

			t.Run("specialized wrapper negative controls", func(t *testing.T) {
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					for _, tc := range []struct{ name, value, token string }{
						{"bare regclass parameter", `($2::regclass)::text`, "$2"},
						{"binary and IO wrapped parameter", `($2::int::oid::regclass)::text`, "$2"},
						{"wrapped special shape", `(COALESCE($2::int,0)::oid::regclass)::text`, "COALESCE"},
					} {
						for _, doc := range []struct {
							name  string
							value any
						}{
							{"selected", `{}`},
							{"unselected", `{"missing":"provided"}`},
							{"SQL NULL root", nil},
						} {
							t.Run(prefix+tc.name+"/"+doc.name, func(t *testing.T) {
								q := prefix + `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v text PATH '$.missing' DEFAULT ` + tc.value + ` ON EMPTY))`
								d := fixture.ControlsReferenceDiagnostic("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT")
								// Pinned transformed-operand position and Prepare admission.
								d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
								sqljsonDiagnostic(t, p, b, c, q, d, true, doc.value, 0)
							})
						}
					}
				}
			})

			t.Run("preceding column reference diagnostics", func(t *testing.T) {
				for _, source := range []string{`(VALUES(7)) AS d(x)`, `jt_review_columns AS d`} {
					for _, prefix := range []string{"", "/*é🙂*/ "} {
						for _, doc := range []string{`'{}'`, `'{"v":9}'`, `NULL::jsonb`} {
							for _, tc := range []struct{ name, value, token, code, message, hint string }{
								{"operator column", `(d.x + 1)`, "d.x", "42804", "DEFAULT expression must not contain column references", ""},
								{"function column", `abs(d.x)`, "abs", "42804", "DEFAULT expression must not contain column references", ""},
								{"bare column", `d.x`, "d.x", "42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", ""},
								{"special shape column", `COALESCE(d.x,0)`, "COALESCE", "42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", ""},
								{"unknown column before shape", `COALESCE(d.missing,0)`, "d.missing", "42703", "column d.missing does not exist", ""},
								{"unknown routine before column restriction", `jt_review_missing(d.x)`, "jt_review_missing", "42883", "function jt_review_missing(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
							} {
								t.Run(source+"/"+prefix+tc.name+"/"+doc, func(t *testing.T) {
									q := prefix + `SELECT j.v FROM ` + source + ` CROSS JOIN JSON_TABLE(` + doc + `,'$' COLUMNS(v int DEFAULT ` + tc.value + ` ON EMPTY)) AS j`
									d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
									d.Hint = tc.hint
									// Pinned binding, enclosing-shape, then column-reference
									// diagnostics, including complete fields and admission stage.
									d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
									sqljsonDiagnostic(t, p, b, c, q, d, true)
								})
							}
						}
					}
				}
			})
		})
	}
}
