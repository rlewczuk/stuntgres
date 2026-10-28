package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBObjectParameters(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT jsonb_object($1) AS v", "SELECT jsonb_object($1,$2) AS v", "SELECT jsonb_object($1,$1) AS v", "SELECT pg_catalog.jsonb_object(VARIADIC $1) AS v", "SELECT pg_catalog.jsonb_object($1, VARIADIC $2) AS v"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				two := q == "SELECT jsonb_object($1,$2) AS v" || q == "SELECT pg_catalog.jsonb_object($1, VARIADIC $2) AS v"
				repeat := q == "SELECT jsonb_object($1,$1) AS v"
				args := func(a, v any) []any {
					if two {
						return []any{a, v}
					}
					return []any{a}
				}
				valid := args("{a,1}", "{x,y}")
				want := `{"a": "1"}`
				if two {
					valid = args("{a,b}", "{1,2}")
					want = `{"a": "1", "b": "2"}`
				}
				if repeat {
					valid = args("{a,b}", nil)
					want = `{"a": "a", "b": "b"}`
				}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				good := jsonbObjectSelect(q, want, valid...)
				good.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, b.PG, "v") }
				run(good)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, good)
				run(jsonbObjectSelect(q, "{}", args("{}", "{}")...))
				run(jsonbObjectSelect(q, nil, args(nil, "{x}")...))
				if two {
					run(jsonbObjectSelect(q, nil, "{a}", nil))
				}
				if !repeat {
					dup := args("{a,old,a,NULL}", "{old,NULL}")
					if two {
						dup = args("{a,a}", "{old,NULL}")
					}
					run(jsonbObjectSelect(q, `{"a": null}`, dup...))
				}
				badArgs := args("{a,b,c}", "{x}")
				msg := "array must have even number of elements"
				if two {
					msg = "mismatched array dimensions"
				}
				if !repeat {
					run(jsonObjectError(q, "2202E", msg, "execute", badArgs...))
				}
				if repeat {
					run(jsonObjectError(q, "22004", "null value not allowed for object key", "execute", "{NULL}"))
				}
				if two {
					run(jsonObjectError(q, "2202E", "wrong number of array subscripts", "execute", "{{a,b}}", "{x,y}"))
				} else if repeat {
					run(jsonObjectError(q, "2202E", "wrong number of array subscripts", "execute", "{{a,1}}"))
				} else {
					run(jsonbObjectSelect(q, `{"a": "1"}`, "{{a,1}}"))
				}
				d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
				d.Detail = `Unexpected "," character.`
				d.Context = "unnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic(q, d, args("{a,,b}", "{x}")...)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				if two {
					d.Context = "unnamed portal parameter $2 = '...'"
					for _, first := range []any{"{a}", nil} {
						s = fixture.ErrorDiagnostic(q, d, first, "{a,,b}")
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
					}
					run(jsonbObjectSelect(q, nil, nil, "{{a,b}}"))
				}
				run(good)
				owned := []byte(valid[0].(string))
				byteArgs := append([]any(nil), valid...)
				byteArgs[0] = owned
				rows, err := stmt.QueryContext(p.Ctx, byteArgs...)
				if err != nil {
					t.Fatal(err)
				}
				var old string
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing byte-input row")
				}
				if err := rows.Scan(&old); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if rows.Next() {
					rows.Close()
					t.Fatal("extra row")
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				rows.Close()
				for i := range owned {
					owned[i] = 'x'
				}
				run(jsonbObjectSelect(q, "{}", args("{}", "{}")...))
				if old != want {
					t.Fatalf("retained output=%q want=%q", old, want)
				}
				run(good)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbo_rebind(id int,a text[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbo_rebind VALUES(1,'{a,1}')", 1))
		q := "SELECT jsonb_object(a) AS v FROM jbo_rebind WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, `{"a": "1"}`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE jbo_rebind ADD COLUMN extra int", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE jbo_rebind SET a='{b,2}' WHERE id=1", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, `{"b": "2"}`, 1))
	})
}
