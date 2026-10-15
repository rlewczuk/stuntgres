package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonEachHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, f := range []string{"json_each", "json_each_text"} {
		for _, target := range []bool{false, true} {
			q := "SELECT * FROM " + f + "($1)"
			if target {
				q = "SELECT " + f + "($1)"
			}
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					input   any
					rows    [][]any
					records [][]any
					bad     bool
				}{
					{`{"a":1,"a":2}`, [][]any{{"a", "1"}, {"a", "2"}}, [][]any{{"(a,1)"}, {"(a,2)"}}, false},
					{`{}`, nil, nil, false},
					{nil, nil, nil, false},
					{`[]`, nil, nil, true},
					{`{"z":3}`, [][]any{{"z", "3"}}, [][]any{{"(z,3)"}}, false},
				} {
					if tc.bad {
						jsonKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("22023", "cannot deconstruct an array as an object"), "execute", tc.input)
						continue
					}
					s := jsonEachSelect(f, q, tc.rows, tc.input)
					if target {
						s = fixture.Select(q, []string{f}, []string{"RECORD"}, tc.records, tc.input)
						s.Bag = true
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonEachWithMetadata(s, b.PG))
				}
				d := fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
				d.Detail, d.Context = `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":{"b":"\u0000...`
				jsonKeysError(t, p, b, held, q, false, d, "execute", `{"a":{"b":"\u0000"}}`)
				s := jsonEachSelect(f, q, [][]any{{"ok", "4"}}, `{"ok":4}`)
				if target {
					s = fixture.Select(q, []string{f}, []string{"RECORD"}, [][]any{{"(ok,4)"}}, `{"ok":4}`)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				// Exec discards the row but retains the SELECT 1 command count.
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, `{"ok":4}`))
			})
		}
	}
}

func TestReferenceJSONEachParametersAndLifecycle(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonEachHeld(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE je_atomic_source(id int,j json)", 0))
		run(fixture.Exec(`INSERT INTO je_atomic_source VALUES(1,'{"ok":1,"n":null}'),(2,'[]')`, 2))
		for _, f := range []string{"json_each", "json_each_text"} {
			t.Run(f, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				dest := "je_dest_" + f
				run(fixture.Exec("CREATE TABLE "+dest+"(k text,v "+jsonEachType(f)+")", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO "+dest+" SELECT f.key,f.value FROM je_atomic_source s,"+f+"(s.j) f ORDER BY s.id", fixture.ControlsReferenceDiagnostic("22023", "cannot deconstruct an array as an object")))
				jsonEachBoth(t, p, b, c, fixture.Select("SELECT k,v FROM "+dest, []string{"k", "v"}, []string{"TEXT", jsonEachType(f)}, nil))
				run(fixture.Prepared(fixture.Exec("INSERT INTO "+dest+" SELECT f.key,f.value FROM je_atomic_source s,"+f+"(s.j) f WHERE s.id=$1", 2, 1)))
				n := any("null")
				if f == "json_each_text" {
					n = nil
				}
				jsonEachBoth(t, p, b, c, fixture.Select("SELECT k,v FROM "+dest+" ORDER BY k", []string{"k", "v"}, []string{"TEXT", jsonEachType(f)}, [][]any{{"n", n}, {"ok", "1"}}))
			})
		}
	})
}
