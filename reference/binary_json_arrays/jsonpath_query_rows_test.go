package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryMetadata(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryFunctions {
			for _, empty := range []bool{false, true} {
				q := `SELECT ` + f.name + `('true','$') AS changed`
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				rows := jsonpathQueryRows(want)
				if empty {
					q += ` WHERE false`
					rows = nil
				}
				s := jsonpathQueryResult(q, f.typ, rows)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					cts, err := r.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(cts) != 1 || cts[0].Name() != "changed" || cts[0].DatabaseTypeName() != f.typ {
						t.Fatalf("metadata before Next: %v", cts)
					}
					if f.typ == "JSONB" {
						jsonbObjectMetadata(t, r, b.PG, "changed")
					}
				}
				jsonpathNavigateBoth(t, p, b, c, s)
			}
		}
	})
}

func TestReferenceJSONPathQueryHeldParameters(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				good := jsonpathQueryOne(q, f.typ, want, `true`, `$`, `{}`, false)
				run(good)
				empty := jsonpathQueryRows(nil)
				if f.name == "jsonb_path_query" {
					empty = nil
				}
				if f.name == "jsonb_path_exists" {
					empty = jsonpathQueryRows(false)
				}
				run(jsonpathQueryResult(q, f.typ, empty, `{}`, `$.missing`, `{}`, true))
				run(jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":true},{}]`, `strict $[*].x`, `{}`, false))
				prefix := want
				if f.name == "jsonb_path_exists" {
					prefix = nil
				}
				run(jsonpathQueryOne(q, f.typ, prefix, `[{"x":true},{}]`, `strict $[*].x`, `{}`, true))
				run(good)
				if !b.PG {
					old, err := stmt.QueryContext(p.Ctx, good.Args...)
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					meta, err := old.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					run(jsonpathQueryResult(q, f.typ, empty, `{}`, `$.missing`, `{}`, true))
					if len(meta) != 1 || meta[0].DatabaseTypeName() != f.typ {
						t.Fatal(meta)
					}
					fixture.StringCoreRetainedRows(t, old, nil, good.Columns, good.Types, good.Rows, false)
				}
			}()
		}
	})
}

func TestReferenceJSONPathQueryFromComposition(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT * FROM jsonb_path_query('[2,3,4]','$[*]')`, []string{"jsonb_path_query"}, []string{"JSONB"}, jsonpathQueryRows(`2`, `3`, `4`)),
			fixture.Select(`SELECT * FROM jsonb_path_query('[2,3,4]','$[*]') WITH ORDINALITY AS q(changed)`, []string{"changed", "ordinality"}, []string{"JSONB", "INT8"}, [][]any{{`2`, int64(1)}, {`3`, int64(2)}, {`4`, int64(3)}}),
			fixture.Select(`SELECT * FROM jsonb_path_query('[2,3,4]','$[*]') WITH ORDINALITY AS q(v,n) WHERE v>'2'::jsonb`, []string{"v", "n"}, []string{"JSONB", "INT8"}, [][]any{{`3`, int64(2)}, {`4`, int64(3)}}),
			fixture.Select(`SELECT * FROM ROWS FROM(jsonb_path_query('[2,3,4]','$[*]'),generate_series(10,11)) WITH ORDINALITY AS q(v,g,n)`, []string{"v", "g", "n"}, []string{"JSONB", "INT4", "INT8"}, [][]any{{`2`, int64(10), int64(1)}, {`3`, int64(11), int64(2)}, {`4`, nil, int64(3)}}),
			fixture.Select(`SELECT * FROM ROWS FROM(jsonb_path_query('{}','$.missing'),generate_series(10,11)) AS q(v,g)`, []string{"v", "g"}, []string{"JSONB", "INT4"}, [][]any{{nil, int64(10)}, {nil, int64(11)}}),
			fixture.Select(`SELECT * FROM ROWS FROM(jsonb_path_query('{}','$.missing'),generate_series(2,1)) AS q(v,g)`, []string{"v", "g"}, []string{"JSONB", "INT4"}, nil),
			fixture.Select(`SELECT v,g FROM jsonb_path_query('[2,3]','$[*]') AS q(v),generate_series(10,11) AS s(g) ORDER BY v,g`, []string{"v", "g"}, []string{"JSONB", "INT4"}, [][]any{{`2`, int64(10)}, {`2`, int64(11)}, {`3`, int64(10)}, {`3`, int64(11)}}),
		} {
			jsonpathNavigateBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpq_lateral(id int,doc jsonb)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpq_lateral VALUES(1,'[2,3]'),(2,'[]'),(3,'[null]')`, 3))
		for _, lateral := range []string{"", "LATERAL "} {
			q := `SELECT d.id,q.v,q.n FROM jpq_lateral d LEFT JOIN ` + lateral + `jsonb_path_query(d.doc,'$[*]') WITH ORDINALITY AS q(v,n) ON true ORDER BY d.id,q.n`
			jsonpathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"id", "v", "n"}, []string{"INT4", "JSONB", "INT8"}, [][]any{{int64(1), `2`, int64(1)}, {int64(1), `3`, int64(2)}, {int64(2), nil, nil}, {int64(3), `null`, int64(1)}}))
		}
	})
}
