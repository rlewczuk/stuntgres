package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbEachHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
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
				for _, input := range []any{`{"a":1,"a":2}`, `{}`, nil, `[]`, `[1]`, `null`, `0`, `true`, `false`, `"x"`, `{"a":2}`} {
					bad := input != nil && input != `{"a":1,"a":2}` && input != `{}` && input != `{"a":2}`
					if bad {
						fixture.JSONKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("22023", "cannot call "+f+" on a non-object"), "execute", input)
						continue
					}
					var rows, records [][]any
					if input == `{"a":1,"a":2}` || input == `{"a":2}` {
						rows = [][]any{{"a", "2"}}
						records = [][]any{{"(a,2)"}}
					}
					s := jsonbEachSelect(f, q, rows, input)
					if target {
						s = fixture.Select(q, []string{f}, []string{"RECORD"}, records, input)
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonEachWithMetadata(s, b.PG))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, `{"ok":4}`))
			})
		}
	}
}

func jsonbEachAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jbeach_atomic_source(id int,j jsonb)", 0))
	run(fixture.Exec(`INSERT INTO jbeach_atomic_source VALUES(1,'{"ok":1,"n":null}'),(2,'[]')`, 2))
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
		t.Run(f+"_atomic", func(t *testing.T) {
			dest := "jbeach_dest_" + f
			run(fixture.Exec("CREATE TABLE "+dest+"(k text,v "+jsonbEachType(f)+")", 0))
			run(fixture.Exec("INSERT INTO "+dest+" VALUES('sentinel','99')", 1))
			q := "INSERT INTO " + dest + " SELECT f.key,f.value FROM jbeach_atomic_source s," + f + "(s.j) f ORDER BY s.id"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			d := fixture.ControlsReferenceDiagnostic("22023", "cannot call "+f+" on a non-object")
			run(fixture.ErrorDiagnostic(q, d))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d))
			run(fixture.Select("SELECT k,v FROM "+dest, []string{"k", "v"}, []string{"TEXT", jsonbEachType(f)}, [][]any{{"sentinel", "99"}}))
			run(fixture.Exec(`UPDATE jbeach_atomic_source SET j='{"z":3}' WHERE id=2`, 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 3))
			n := any("null")
			if f == "jsonb_each_text" {
				n = nil
			}
			run(fixture.Select("SELECT k,v FROM "+dest+" ORDER BY k", []string{"k", "v"}, []string{"TEXT", jsonbEachType(f)}, [][]any{{"n", n}, {"ok", "1"}, {"sentinel", "99"}, {"z", "3"}}))
			run(fixture.Exec(`UPDATE jbeach_atomic_source SET j='[]' WHERE id=2`, 1))
		})
	}
}

func TestReferenceJSONBEachParametersAndLifecycle(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbEachHeld(t, p, b, c)
		jsonbEachAtomic(t, p, b, c)
	})
}
