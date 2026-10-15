package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbContainmentScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func jsonbContainmentHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, op := range []string{"@>", "<@"} {
		t.Run("held "+op, func(t *testing.T) {
			q := "SELECT $1::jsonb " + op + " $2::jsonb AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				a, z any
				want any
			}{{`{"a":1,"b":2}`, `{"a":1}`, true}, {`{"a":2}`, `{"a":1}`, false}, {nil, `{}`, nil}, {`{}`, nil, nil}} {
				a, z := tc.a, tc.z
				if op == "<@" {
					a, z = z, a
				}
				s := jsonbExistsSelect(q, tc.want, a, z)
				s.CheckMetadata = jsonbExistsMetadata
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, parameter := range []int{1, 2} {
				args := []any{nil, nil}
				args[parameter-1] = "bad"
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Token "bad" is invalid.`
				d.Context = "JSON data, line 1: bad\nunnamed portal parameter $" + fixture.Position(parameter) + " = '...'"
				s := fixture.ErrorDiagnostic(q, d, args...)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			s := jsonbExistsSelect(q, true, `{}`, `{}`)
			s.CheckMetadata = jsonbExistsMetadata
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		})
	}
}

func jsonbContainmentRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jbc_values(id int,doc jsonb)", 0))
	run(fixture.Exec(`INSERT INTO jbc_values VALUES(1,'{"a":1,"b":2}'),(2,'{"a":2}'),(3,NULL)`, 3))
	for _, predicate := range []string{`doc @> '{"a":1}'::jsonb`, `'{"a":1}'::jsonb <@ doc`} {
		jsonbExistsBoth(t, p, b, c, fixture.Select("SELECT id FROM jbc_values WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	jsonbExistsBoth(t, p, b, c, fixture.Select(`SELECT id,doc @> '{"a":1}'::jsonb AS v FROM jbc_values ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
	for _, suffix := range []string{"", " WHERE FALSE"} {
		var rows [][]any
		if suffix == "" {
			rows = [][]any{{true}, {false}, {nil}}
		}
		s := fixture.Select(`SELECT doc @> '{"a":1}'::jsonb AS v FROM jbc_values`+suffix+" ORDER BY id", []string{"v"}, []string{"BOOL"}, rows)
		s.CheckMetadata = jsonbExistsMetadata
		jsonbExistsBoth(t, p, b, c, s)
	}
	jsonbExistsBoth(t, p, b, c, fixture.Select(`SELECT doc @> '{}'::jsonb AS v FROM jbc_values WHERE id<3 ORDER BY id`, []string{"v"}, []string{"BOOL"}, [][]any{{true}, {true}}))
}
