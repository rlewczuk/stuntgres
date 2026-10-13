package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Both available backends execute the same SQL and independent assertions.
func jsonArrayLengthScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func jsonArrayLengthBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func jsonArrayLengthSelect(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{value}}, args...)
}

func jsonArrayLengthError(q, message string, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22023", message), args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func jsonArrayLengthMetadata(t *testing.T, rows *sql.Rows, pg bool, name string) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0] != name || len(cts) != 1 {
		t.Fatalf("columns=%v types=%v", columns, cts)
	}
	ct := cts[0]
	scan := reflect.TypeFor[int64]()
	if pg {
		scan = reflect.TypeFor[int32]()
	}
	if ct.Name() != name || ct.DatabaseTypeName() != "INT4" || ct.ScanType() != scan {
		t.Fatalf("metadata=%q/%q/%v want=%q/INT4/%v", ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), name, scan)
	}
	if n, ok := ct.Length(); ok {
		t.Fatalf("unexpected length %d", n)
	}
	if p, s, ok := ct.DecimalSize(); ok {
		t.Fatalf("unexpected decimal %d,%d", p, s)
	}
	if n, ok := ct.Nullable(); n || ok {
		t.Fatalf("nullable=%v,%v want false,false", n, ok)
	}
}

func TestReferenceJSONArrayLengthValues(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"json_array_length", "jsonb_array_length"} {
			for _, tc := range []struct {
				input string
				n     int64
			}{
				{"[]", 0}, {`[1,2,3,{"f1":1,"f2":[5,6]},4]`, 5},
				{"[null,null]", 2}, {`[[],[1,2],{"a":[3]}]`, 3},
				{` [ "a,b][", "quote\"slash\\", {"x":1,"x":[2,3]} ] `, 3},
			} {
				t.Run(f+" "+tc.input, func(t *testing.T) {
					jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT "+f+"('"+tc.input+"') AS n", tc.n))
				})
			}
			for _, arg := range []string{"NULL", "NULL::" + strings.TrimSuffix(f, "_array_length")} {
				jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT "+f+"("+arg+") AS n", nil))
			}
		}
		// These are valid retained JSON, but not valid JSONB inputs. Counting
		// must not silently convert the source to JSONB or decode its strings.
		for _, input := range []string{`["\u0000","\ud800"]`, `[1e1000000]`, strings.Repeat("[", 256) + "0" + strings.Repeat("]", 256)} {
			want := int64(1)
			if input == `["\u0000","\ud800"]` {
				want = 2
			}
			jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT json_array_length($1) AS n", want, input))
		}
	})
}

func TestReferenceJSONArrayLengthDiagnostics(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, f := range []string{"json_array_length", "jsonb_array_length"} {
			for _, input := range []string{"{}", "4", "true", "false", `"str"`, "null"} {
				message := "cannot get array length of a scalar"
				if input == "{}" {
					message = "cannot get array length of a non-array"
				}
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(f+" "+prefix+input, func(t *testing.T) {
						jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthError(prefix+"SELECT "+f+"('"+input+"')", message))
					})
				}
			}
			for _, prefix := range []string{"", "/*é*/ "} {
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail, d.Context = `Expected JSON value, but found "]".`, "JSON data, line 1: [1,]"
				pos := 26
				if f == "jsonb_array_length" {
					pos++
				}
				if prefix != "" {
					pos += 6
				}
				d.Position = fixture.Position(pos)
				s := fixture.ErrorDiagnostic(prefix+"SELECT "+f+"('[1,]')", d)
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
		}
		run(fixture.Exec("CREATE TABLE jal_demand(j json,b jsonb)", 0))
		for _, tc := range []struct{ f, col string }{{"json_array_length", "j"}, {"jsonb_array_length", "b"}} {
			for _, q := range []string{"SELECT " + tc.f + "('{}') FROM jal_demand", "SELECT " + tc.f + "('{}') WHERE FALSE"} {
				jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthError(q, "cannot get array length of a non-array"))
			}
			jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT "+tc.f+"("+tc.col+") AS n FROM jal_demand", []string{"n"}, []string{"INT4"}, nil))
		}
		run(fixture.Exec("INSERT INTO jal_demand VALUES('{}','{}')", 1))
		for _, tc := range []struct{ f, col string }{{"json_array_length", "j"}, {"jsonb_array_length", "b"}} {
			jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT "+tc.f+"("+tc.col+") AS n FROM jal_demand WHERE FALSE", []string{"n"}, []string{"INT4"}, nil))
		}
		for _, prefix := range []string{"", "/*é*/ "} {
			q := prefix + "SELECT json_array_length("
			d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			d.Position = fixture.Position(len([]rune(q)) + 1)
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
	})
}

func TestReferenceJSONArrayLengthTyping(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, family := range []string{"json", "jsonb"} {
			f := family + "_array_length"
			other := "jsonb"
			if family == "jsonb" {
				other = "json"
			}
			run(fixture.Exec("CREATE DOMAIN jal_"+family+" AS "+family, 0))
			for _, call := range []string{
				f + "('[1]'::" + family + ")", "pg_catalog." + f + "('[1]')", `"` + f + `"('[1]')`,
				strings.ToUpper(f) + "('[1]')", f + "('[1]'::jal_" + family + ")", f + "('[1]'::" + other + "::" + family + ")",
			} {
				jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT "+call+" AS n", int64(1)))
			}
			for _, tc := range []struct{ arg, sig string }{
				{"1", "integer"}, {"'[]'::text", "text"}, {"ARRAY[1,2]", "integer[]"},
				{"'[]'::" + other, other}, {"", ""}, {"'[]','[]'", "unknown, unknown"},
				{"a => '[]'::" + family, "a => " + family},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(prefix+f+" "+tc.sig, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("42883", "function "+f+"("+tc.sig+") does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						pos := 8
						if prefix != "" {
							pos += 6
						}
						d.Position = fixture.Position(pos)
						s := fixture.ErrorDiagnostic(prefix+"SELECT "+f+"("+tc.arg+")", d)
						s.SelectRows, s.ErrorStage = true, "execute"
						run(s)
						s.Prepared, s.ErrorStage = true, "prepare"
						run(s)
					})
				}
			}
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+strings.ToUpper(f)+"(unknown) does not exist")
			d.Hint, d.Position = "No function matches the given name and argument types. You might need to add explicit type casts.", "8"
			s := fixture.ErrorDiagnostic(`SELECT "`+strings.ToUpper(f)+`"('[]')`, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
		d := fixture.ControlsReferenceDiagnostic("3F000", `schema "missing_jal" does not exist`)
		d.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT missing_jal.json_array_length('[]')", d)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
	})
}

func jsonArrayLengthFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, f string) {
	t.Helper()
	q := "SELECT " + f + "($1) AS n"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	for _, tc := range []struct {
		input any
		n     any
	}{{"[]", int64(0)}, {`[1,2,3,{"f1":1,"f2":[5,6]},4]`, int64(5)}, {nil, nil}} {
		s := jsonArrayLengthSelect(q, tc.n, tc.input)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayLengthMetadata(t, r, b.PG, "n") }
		run(s)
	}
	run(jsonArrayLengthError(q, "cannot get array length of a non-array", "{}"))
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
	d.Detail = `Expected JSON value, but found "]".`
	d.Context = "JSON data, line 1: [1,]\nunnamed portal parameter $1 = '...'"
	s := fixture.ErrorDiagnostic(q, d, "[1,]")
	s.SelectRows, s.ErrorStage = true, "execute"
	run(s)
	run(jsonArrayLengthSelect(q, int64(1), "[null]"))
	for _, input := range []string{"null", "4", "true", "false", `"str"`} {
		run(jsonArrayLengthError(q, "cannot get array length of a scalar", input))
	}
	run(jsonArrayLengthSelect(q, int64(0), "[]"))
}

func TestReferenceJSONArrayLengthParameters(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"json_array_length", "jsonb_array_length"} {
			t.Run(f, func(t *testing.T) { jsonArrayLengthFrames(t, p, b, c, f) })
			q := "SELECT " + f + "($1) AS n WHERE FALSE"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			// PostgreSQL's first custom plan folds the bound immutable call.
			// Native parameters remain runtime-owned: the false filter prevents
			// evaluation. Assert both outcomes, not parity or a skipped route.
			s := fixture.Select(q, []string{"n"}, []string{"INT4"}, nil, "{}")
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				jsonArrayLengthMetadata(t, rows, b.PG, "n")
			}
			if b.PG {
				s = jsonArrayLengthError(q, "cannot get array length of a non-array", "{}")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT json_array_length($1) AS j,jsonb_array_length($2) AS b", []string{"j", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}, "[null]", "[[],{}]"))
	})
}

func TestReferenceJSONArrayLengthMetadata(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jal_meta(j json,b jsonb)", 0))
		for _, tc := range []struct{ f, col, family string }{{"json_array_length", "j", "json"}, {"jsonb_array_length", "b", "jsonb"}} {
			for _, data := range []struct {
				q, name string
				values  [][]any
			}{
				{"SELECT " + tc.f + "('[]')", tc.f, [][]any{{int64(0)}}},
				{"SELECT " + tc.f + "(NULL::" + tc.family + ") AS n", "n", [][]any{{nil}}},
				{"SELECT " + tc.f + "(" + tc.col + ") AS n FROM jal_meta", "n", nil},
			} {
				s := fixture.Select(data.q, []string{data.name}, []string{"INT4"}, data.values)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayLengthMetadata(t, r, b.PG, data.name) }
				s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
					want := data.values[i][0]
					var raw any
					if err := r.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(raw, want) {
						t.Fatalf("raw=%T %v want=%T %v", raw, raw, want, want)
					}
					var n sql.NullInt64
					if err := r.Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n.Valid != (want != nil) || (n.Valid && n.Int64 != want.(int64)) {
						t.Fatalf("NullInt64=%v want=%v", n, want)
					}
					if want != nil {
						var v int64
						if err := r.Scan(&v); err != nil || v != want {
							t.Fatalf("int64=%d err=%v want=%v", v, err, want)
						}
					}
				}
				jsonArrayLengthBoth(t, p, b, c, s)
			}
		}
	})
}

func TestReferenceJSONArrayLengthACL(t *testing.T) {
	const role = "jal_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	fixture.SchemaReferenceCleanup(t, "jal_private")
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE SCHEMA jal_private", 0))
		run(fixture.Exec("CREATE TABLE jal_acl(j json,b jsonb)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny := func(q string) {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jal_acl"))
			s.SelectRows, s.ErrorStage = true, "execute"
			jsonArrayLengthBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT json_array_length(j) FROM jal_acl", "SELECT jsonb_array_length(b) FROM jal_acl WHERE FALSE"} {
			deny(q)
		}
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema jal_private")
		d.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT jal_private.json_array_length('[]')", d)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO jal_acl VALUES('[1,2]','[1]')", 1))
		run(fixture.Exec("GRANT SELECT(j) ON jal_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT json_array_length(j) AS n FROM jal_acl", int64(2)))
		deny("SELECT jsonb_array_length(b) FROM jal_acl")
		deny("SELECT jsonb_array_length(b) FROM jal_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(b) ON jal_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonArrayLengthBoth(t, p, b, c, jsonArrayLengthSelect("SELECT jsonb_array_length(b) AS n FROM jal_acl", int64(1)))
	})
}
