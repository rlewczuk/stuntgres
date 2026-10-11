package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceBindsAndComposition(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			apply(t, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
			apply(t, fixture.Exec("INSERT INTO t VALUES(1,'abcdef','XY',3,2),(2,'abcdef','XY',3,2)", 2))
			for _, backend := range p.Backends() {
				const query = "SELECT overlay($1::bytea,$2::bytea,$3,$4) AS o,substr($1::bytea,$3,$4) AS s,pg_catalog.position($1::bytea,$2::bytea) AS p"
				stmt, err := backend.DB.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				if nonContext {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					stmt, err = backend.DB.Prepare(query)
					if err != nil {
						t.Fatal(err)
					}
				}
				defer stmt.Close()
				for _, tc := range []struct {
					a, b              any
					s, n              int
					overlay, sub, pos any
				}{{[]byte("abcdef"), []byte("XY"), 3, 2, "abXYef", "cd", int64(0)}, {[]byte{}, []byte{}, 1, 0, "", "", int64(1)}, {[]byte(nil), []byte("x"), 1, 1, nil, nil, nil}, {`\x41`, []byte("x"), 2, 1, `\x41`, "x", int64(2)}} {
					step := fixture.Select("SELECT overlay($1::bytea,$2::bytea,$3,$4) AS o,substr($1::bytea,$3,$4) AS s,pg_catalog.position($1::bytea,$2::bytea) AS p", []string{"o", "s", "p"}, []string{"BYTEA", "BYTEA", "INT4"}, [][]any{{tc.overlay, tc.sub, tc.pos}}, tc.a, tc.b, tc.s, tc.n)
					run(t, p.Ctx, backend.DB, backend.Recorder, step)
					if nonContext {
						rows, err := stmt.Query(tc.a, tc.b, tc.s, tc.n)
						fixture.DeleteReferenceConsume(t, backend.PG, rows, err, step)
					} else {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, step)
					}
				}
			}
			for _, q := range []string{"SELECT substr(v,i,n) AS s,position(s IN v) AS p FROM t WHERE id=99", "SELECT substr(NULL::bytea,i,n) AS s,position(NULL::bytea IN v) AS p FROM t WHERE id=1"} {
				rows := [][]any{}
				if q == "SELECT substr(NULL::bytea,i,n) AS s,position(NULL::bytea IN v) AS p FROM t WHERE id=1" {
					rows = [][]any{{nil, nil}}
				}
				step := fixture.Select(q, []string{"s", "p"}, []string{"BYTEA", "INT4"}, rows)
				apply(t, step)
				apply(t, fixture.Prepared(step))
			}
			steps := []fixture.Step{
				fixture.Select("SELECT overlay(v,s,i,n) AS o,count(*) AS c FROM t GROUP BY overlay(v,s,i,n) HAVING pg_catalog.position(overlay(v,s,i,n),'XY')=3", []string{"o", "c"}, []string{"BYTEA", "INT8"}, [][]any{{"abXYef", int64(2)}}),
				fixture.Select("SELECT id,CASE WHEN position(s IN v)=0 THEN substr(overlay(v,s,i,n),i,n) ELSE v END AS v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "XY"}, {int64(2), "XY"}}),
			}
			for _, step := range steps {
				apply(t, step)
				apply(t, fixture.Prepared(step))
			}
			apply(t, fixture.Exec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, fixture.Exec("INSERT INTO dst SELECT substring(v FROM i FOR n) FROM t WHERE pg_catalog.position(v,'cd')=3", 2))
			apply(t, fixture.Select("UPDATE dst SET v=overlay(v,'X',1,1) RETURNING v", []string{"v"}, []string{"BYTEA"}, [][]any{{"Xd"}, {"Xd"}}))
			apply(t, fixture.Select("DELETE FROM dst RETURNING substring(v FROM 2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{"d"}, {"d"}}))
		})
	}
}
