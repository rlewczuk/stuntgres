package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSJSONBinding(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"json", "jsonb"} {
			name := typ + "_to_tsvector"
			for _, tc := range []struct{ operands, types string }{
				{"", ""},
				{"'{}'::" + typ, typ},
				{"true,'[]'::jsonb", "boolean, jsonb"},
				{"'{}'::" + typ + ",true", typ + ", boolean"},
				{"NULL::regconfig,true,'[]'::jsonb", "regconfig, boolean, jsonb"},
				{"'simple'::regconfig,'{}'::" + typ + ",'[]'::jsonb,true", "regconfig, " + typ + ", jsonb, boolean"},
				{"document => '{}'::" + typ + ",filter => '[]'::jsonb", "document => " + typ + ", filter => jsonb"},
				{"'{}'::" + typ + ",VARIADIC ARRAY['[]'::jsonb]", typ + ", jsonb[]"},
			} {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+tc.types+") does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				for _, prefix := range []string{"", "/*é😀*/ "} {
					d.Position = fmt.Sprint(8 + len([]rune(prefix)))
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(prefix+"SELECT "+name+"("+tc.operands+")", d)
						s.Prepared, s.SelectRows = prepared, true
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			}
			for _, tc := range []struct{ sql, message, position string }{
				{"SELECT " + name + "(DISTINCT '{}'::" + typ + ",'[]'::jsonb)", "DISTINCT specified, but " + name + " is not an aggregate function", "8"},
				{"SELECT " + name + "('{}'::" + typ + ",'[]'::jsonb ORDER BY 1)", "ORDER BY specified, but " + name + " is not an aggregate function", "8"},
				{"SELECT " + name + "('{}'::" + typ + ",'[]'::jsonb) FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function", "8"},
			} {
				d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
				d.Position = tc.position
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.sql, d)
					s.Prepared, s.SelectRows = prepared, true
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			// Binding/input errors cannot be hidden by strict document NULL.
			form := ftsJSONForm{name, typ, true, true}
			query, args := form.query(false, "json_missing", nil, `[]`)
			d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "json_missing" does not exist`)
			d.Context = "unnamed portal parameter $1 = '...'"
			ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(query, d, args...))
			query, args = form.query(false, "english", nil, "oops")
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			d = fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail = `Token "oops" is invalid.`
			d.Context = "JSON data, line 1: oops\nunnamed portal parameter $3 = '...'"
			ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, d, args...))
			_, args = form.query(false, "english", `"cat"`, `"string"`)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'cat':1", args...))
			stmt.Close()
			literal, _ := form.query(true, "english", nil, "oops")
			d.Context = "JSON data, line 1: oops"
			d.Position = fmt.Sprint(strings.Index(literal, "'oops'") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(literal, d)
				s.Prepared, s.SelectRows = prepared, true
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferenceFTSJSONAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"json_fts_actor"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE json_fts_actor", 0))
		signature := func(f ftsJSONForm) string {
			types := f.typ
			if f.explicit {
				types = "regconfig," + types
			}
			if f.filtered {
				types += ",jsonb"
			}
			return "pg_catalog." + f.name + "(" + types + ")"
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
			for _, f := range ftsJSONForms() {
				if _, err := c.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION "+signature(f)+" TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}
		}()
		for _, f := range ftsJSONForms() {
			query, args := f.query(false, "english", `"cat"`, `"string"`)
			query = strings.Replace(query, "SELECT ", "SELECT pg_catalog.", 1)
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature(f)+" FROM PUBLIC", 0))
			run(fixture.Exec("SET ROLE json_fts_actor", 0))
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+f.name), args...))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature(f)+" TO PUBLIC", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'cat':1", args...))
			stmt.Close()
		}
	})
}
