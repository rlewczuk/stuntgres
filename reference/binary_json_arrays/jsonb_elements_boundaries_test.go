package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsRaw(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, prepared := range []bool{false, true} {
				q := "SELECT * FROM " + f + "($1) WITH ORDINALITY AS e(v,n) ORDER BY n"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx, `[1,null,"x"]`)
				} else {
					rows, err = c.QueryContext(p.Ctx, q, `[1,null,"x"]`)
				}
				if err != nil {
					t.Fatal(err)
				}
				want := []any{"1", "null", `"x"`}
				if jsonbElementsType(f) == "TEXT" {
					want = []any{"1", nil, "x"}
				}
				for i, v := range want {
					if !rows.Next() {
						rows.Close()
						t.Fatalf("missing row %d: %v", i, rows.Err())
					}
					var raw, n any
					if err := rows.Scan(&raw, &n); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if raw != nil {
						fixture.AssertRawValue(t, b.PG, jsonbElementsType(f), raw)
					}
					if !reflect.DeepEqual(fixture.Normalize(raw), v) || n != int64(i+1) {
						t.Fatalf("raw=%#v/%#v want=%#v/%d", raw, n, v, i+1)
					}
				}
				if rows.Next() {
					rows.Close()
					t.Fatal("extra raw row")
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
		}
	})
}

func TestReferenceJSONBElementsStoredPlacement(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbe_placement(j jsonb)", 0))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, tc := range []struct{ q, message string }{
				{"CREATE TABLE jbe_default(v " + jsonbElementsType(f) + " DEFAULT " + f + "('[1]'))", "set-returning functions are not allowed in DEFAULT expressions"},
				{"CREATE INDEX jbe_idx ON jbe_placement ((" + f + "(j)))", "set-returning functions are not allowed in index expressions"},
				{"CREATE TABLE jbe_check(j jsonb CHECK (" + f + "(j) IS NOT NULL))", "set-returning functions are not allowed in check constraints"},
				{"CREATE TABLE jbe_generated(j jsonb,v " + jsonbElementsType(f) + " GENERATED ALWAYS AS (" + f + "(j)) STORED)", "set-returning functions are not allowed in column generation expressions"},
			} {
				// PostgreSQL analyzes these utility-statement expressions at
				// execution, including the held prepared execution route.
				jsonTypeBoth(t, p, b, c, jsonTypeError(tc.q, "0A000", tc.message, strings.Index(tc.q, f)+1))
			}
		}
	})
}

func TestReferenceJSONBElementsAuthority(t *testing.T) {
	const role = "jbe_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jbe_acl(j jsonb,hidden jsonb)", 0))
		run(fixture.Exec(`INSERT INTO jbe_acl VALUES('[1]','[2]')`, 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, "SELECT e.value FROM jbe_acl s,"+f+"(s.j) e", prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbe_acl"), "execute")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(j) ON jbe_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			jsonTypeBoth(t, p, b, c, jsonbElementsSelect(f, "SELECT e.value FROM jbe_acl s,"+f+"(s.j) e", []string{"value"}, [][]any{{"1"}}))
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, "SELECT e.value FROM jbe_acl s,"+f+"(s.hidden) e WHERE false", prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbe_acl"), "execute")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
