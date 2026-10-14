package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSRFFromLateral(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const left = "(VALUES(1,2),(2,1),(3,NULL),(4,0),(5,2)) s(id,hi)"
		for _, explicit := range []string{"", "LATERAL "} {
			t.Run(explicit, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select("SELECT id,v,n FROM "+left+","+explicit+"generate_series(1,s.hi) WITH ORDINALITY g(v,n) ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(1)}, {int64(5), int64(1), int64(1)}, {int64(5), int64(2), int64(2)}}))
				seriesBoth(t, p, b, c, fixture.Select("SELECT id,v,n FROM "+left+" LEFT JOIN "+explicit+"generate_series(1,s.hi) WITH ORDINALITY g(v,n) ON true ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(1)}, {int64(3), nil, nil}, {int64(4), nil, nil}, {int64(5), int64(1), int64(1)}, {int64(5), int64(2), int64(2)}}))
			})
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a.v,b.w,b.n FROM generate_series(1,2) a(v),generate_series(1,a.v) WITH ORDINALITY b(w,n) ORDER BY 1,3", []string{"v", "w", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}}),
			fixture.Select("SELECT id,a,b,n FROM (VALUES(1,2),(2,1)) s(id,hi),ROWS FROM(generate_series(1,s.hi),generate_series(10,10+s.hi)) WITH ORDINALITY r(a,b,n) ORDER BY id,n", []string{"id", "a", "b", "n"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(10), int64(1)}, {int64(1), int64(2), int64(11), int64(2)}, {int64(1), nil, int64(12), int64(3)}, {int64(2), int64(1), int64(10), int64(1)}, {int64(2), nil, int64(11), int64(2)}}),
			fixture.Select("SELECT p.hi,l.lo,g.v,g.n FROM (VALUES(2)) p(hi),(VALUES(1)) l(lo) JOIN LATERAL generate_series(l.lo,p.hi) WITH ORDINALITY g(v,n) ON true ORDER BY n", []string{"hi", "lo", "v", "n"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(2), int64(2)}}),
		} {
			t.Run(s.Query, func(t *testing.T) { seriesBoth(t, p, b, c, s) })
		}
		for _, join := range []string{"RIGHT", "FULL"} {
			seriesBoth(t, p, b, c, fixture.Select("SELECT s.id,g.v,g.n FROM (VALUES(1)) s(id) "+join+" JOIN LATERAL generate_series(2,3) WITH ORDINALITY g(v,n) ON false ORDER BY g.n NULLS LAST", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, func() [][]any {
				rows := [][]any{{nil, int64(2), int64(1)}, {nil, int64(3), int64(2)}}
				if join == "FULL" {
					rows = append(rows, []any{int64(1), nil, nil})
				}
				return rows
			}()))
		}
	})
}

func TestReferenceSRFFromHeldParameters(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"integer", "bigint"} {
			t.Run(typ, func(t *testing.T) {
				q := "SELECT * FROM generate_series($1,3::" + typ + ",$2) WITH ORDINALITY g(v,n) ORDER BY n"
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, tc := range []struct {
					start any
					step  int64
					rows  [][]any
				}{
					{int64(1), 1, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}},
					{int64(3), 1, [][]any{{int64(3), int64(1)}}}, {int64(4), 1, nil}, {nil, 1, nil}, {int64(1), 0, nil},
					{int64(1), 1, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}},
				} {
					s := fixture.Select(q, []string{"v", "n"}, []string{map[string]string{"integer": "INT4", "bigint": "INT8"}[typ], "INT8"}, tc.rows, tc.start, tc.step)
					if tc.step == 0 {
						s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""), tc.start, tc.step)
						s.SelectRows, s.ErrorStage = true, "execute"
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
		const q = "SELECT * FROM ROWS FROM(generate_series(1,$1),generate_subscripts($2::integer[],1)) WITH ORDINALITY g(v,i,n) ORDER BY n"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, s := range []fixture.Step{
			fixture.Select(q, []string{"v", "i", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(1)}, {int64(2), int64(1), int64(2)}, {int64(3), nil, int64(3)}}, int64(3), "[0:1]={NULL,NULL}"),
			fixture.Select(q, []string{"v", "i", "n"}, []string{"INT4", "INT4", "INT8"}, nil, int64(0), "{}"),
			fixture.Select(q, []string{"v", "i", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}}, int64(1), "{9}"),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
