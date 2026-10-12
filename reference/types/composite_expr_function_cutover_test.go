package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCompositeExprFunctionSourceCutover(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			run := func(s fixture.Step) {
				t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) })
			}
			run(fixture.Exec("CREATE TABLE composite_function_empty(i integer)", 0))
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT f::text AS v FROM regexp_count('abba','b') f", []string{"v"}, []string{"TEXT"}, [][]any{{"2"}}),
					fixture.Select("SELECT f::text AS v FROM regexp_like('abba','b') f", []string{"v"}, []string{"TEXT"}, [][]any{{"true"}}),
					fixture.Select("SELECT f::text AS v FROM regexp_split_to_table('a,b',',') f ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}),
					fixture.Select("SELECT f::text AS v,ROW(f.*)::text AS r FROM regexp_match('ab','(a)(b)') f", []string{"v", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"{a,b}", `("{a,b}")`}}),
					fixture.Select("SELECT f::text AS v,ROW(f.*)::text AS r FROM ROWS FROM (regexp_matches('ab','(a)(b)')) f", []string{"v", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"{a,b}", `("{a,b}")`}}),
					fixture.Select("SELECT f::text AS v,num_nulls(f) AS n,ROW(f.*)::text AS r FROM regexp_match('ab','z') f", []string{"v", "n", "r"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{nil, int64(1), "()"}}),
					fixture.Select("SELECT k.i,f::text AS v,num_nulls(f) AS n FROM (VALUES(1),(2)) k(i) LEFT JOIN regexp_matches(CASE WHEN k.i=1 THEN 'ab' ELSE 'z' END,'(a)(b)') f ON TRUE ORDER BY k.i", []string{"i", "v", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "{a,b}", int64(0)}, {int64(2), nil, int64(1)}}),
					fixture.Select("SELECT f.* FROM regexp_count('abba','b') f", []string{"f"}, []string{"INT4"}, [][]any{{int64(2)}}),
					fixture.Select("SELECT f::text AS v FROM regexp_matches('ab','(a)(b)') WITH ORDINALITY f", []string{"v"}, []string{"TEXT"}, [][]any{{"{a,b}"}}),
					fixture.Select("SELECT f::text AS v FROM regexp_matches('ab','(a)(b)') WITH ORDINALITY f(v,n)", []string{"v"}, []string{"TEXT"}, [][]any{{`("{a,b}",1)`}}),
					fixture.Select("SELECT k.i,f::text AS v,num_nulls(f) AS n FROM (VALUES(1),(2)) k(i) LEFT JOIN regexp_matches(CASE WHEN k.i=1 THEN 'ab' ELSE 'z' END,'(a)(b)') WITH ORDINALITY f(v,n) ON TRUE ORDER BY k.i", []string{"i", "v", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), `("{a,b}",1)`, int64(0)}, {int64(2), nil, int64(1)}}),
					fixture.Select("SELECT f::text AS v FROM ROWS FROM(regexp_match('ab','(a)(b)'),regexp_count('ab','a')) f", []string{"v"}, []string{"TEXT"}, [][]any{{`("{a,b}",1)`}}),
					// The inner ordinal tuple is demanded through both the nested
					// join and a later function argument, not just a final projection.
					fixture.Select("SELECT k.i,f::text AS v,num_nulls(f.*) AS absent,g AS witness FROM (VALUES(1),(2)) k(i) LEFT JOIN ((VALUES(7)) q(j) LEFT JOIN regexp_matches(CASE WHEN k.i=1 THEN 'ab' ELSE 'z' END,'(a)(b)') WITH ORDINALITY f(v,n) ON TRUE) ON TRUE,regexp_count(f::text,'a') g ORDER BY k.i", []string{"i", "v", "absent", "witness"}, []string{"INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), `("{a,b}",1)`, int64(0), int64(1)}, {int64(2), nil, int64(1), nil}}),
					// p has an all-NULL cell when present. Its whole row is used
					// solely as a function argument, so NULL cells cannot stand
					// in for the occurrence flag.
					fixture.Select("SELECT k.i,g AS witness FROM (VALUES(1),(2)) k(i) LEFT JOIN (VALUES(NULL::text)) p(x) ON k.i=1,regexp_count(p::text,'[()]') g ORDER BY k.i", []string{"i", "witness"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), nil}}),
					// The nested invocation must inherit p's occurrence flags
					// as well as its row cells from the parent's prefix.
					fixture.Select("SELECT k.i,f::text AS v,num_nulls(f.*) AS absent FROM ((VALUES(1),(2)) k(i) LEFT JOIN (VALUES(NULL::text)) p(x) ON k.i=1) LEFT JOIN ((VALUES(7)) q(j) LEFT JOIN regexp_matches(COALESCE(p::text,'ab'),'(a)(b)') WITH ORDINALITY f(v,n) ON TRUE) ON TRUE ORDER BY k.i", []string{"i", "v", "absent"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(1)}, {int64(2), `("{a,b}",1)`, int64(0)}}),
					// Compile the demanded right layout even with no left row,
					// but never evaluate its row-dependent failing argument.
					fixture.Select("SELECT f::text AS v,num_nulls(f.*) AS absent FROM composite_function_empty e LEFT JOIN regexp_matches((1/e.i)::text,'(.)') WITH ORDINALITY f(v,n) ON TRUE", []string{"v", "absent"}, []string{"TEXT", "INT4"}, [][]any{}),
				} {
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
			}
			for _, q := range []string{
				"CREATE VIEW composite_function_cutover_v AS SELECT f::text AS v,ROW(f.*)::text AS r FROM regexp_match('ab','(a)(b)') f",
				"CREATE VIEW composite_function_cutover_t AS SELECT f::text AS v FROM regexp_matches('ab','(a)(b)') WITH ORDINALITY f(v,n)",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT v,r FROM composite_function_cutover_v", []string{"v", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"{a,b}", `("{a,b}")`}}),
					fixture.Select("SELECT v FROM composite_function_cutover_t", []string{"v"}, []string{"TEXT"}, [][]any{{`("{a,b}",1)`}}),
				} {
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
			}
		})
	}
}
