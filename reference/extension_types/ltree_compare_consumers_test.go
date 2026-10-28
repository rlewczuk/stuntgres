package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeComparisonConsumerChecks(b fixture.Backend) []fixture.Step {
	return []fixture.Step{
		ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v,id", 4,3,2,6,1,5),
		ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v DESC,id", 5,1,2,6,3,4),
		ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v NULLS FIRST,id", 5,4,3,2,6,1),
		ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v DESC NULLS LAST,id", 1,2,6,3,4,5),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id", 2,6),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v<'a-b'::ltree ORDER BY id", 2,3,4,6),
		fixture.Select("SELECT count(*) AS n FROM (SELECT DISTINCT v FROM lt_cmp) AS d", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}),
		fixture.Select("SELECT a.id AS l,b.id AS r FROM lt_cmp a JOIN lt_cmp b ON a.v=b.v ORDER BY a.id,b.id", []string{"l","r"}, []string{"INT4","INT4"}, [][]any{{int64(1),int64(1)},{int64(2),int64(2)},{int64(2),int64(6)},{int64(3),int64(3)},{int64(4),int64(4)},{int64(6),int64(2)},{int64(6),int64(6)}}),
		fixture.Select("SELECT id,v IN ('a.b'::ltree,NULL::ltree) AS member,v NOT IN ('a.b'::ltree,NULL::ltree) AS absent,CASE v WHEN 'a.b'::ltree THEN true ELSE false END AS chosen,NULLIF(v,'a.b'::ltree) IS NULL AS nullified,v IS NOT DISTINCT FROM NULL::ltree AS null_value,v IS DISTINCT FROM 'a.b'::ltree AS different FROM lt_cmp ORDER BY id",
			[]string{"id","member","absent","chosen","nullified","null_value","different"}, []string{"INT4","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},
			[][]any{{int64(1),nil,nil,false,false,false,true},{int64(2),true,false,true,true,false,false},{int64(3),nil,nil,false,false,false,true},{int64(4),nil,nil,false,false,false,true},{int64(5),nil,nil,false,true,true,true},{int64(6),true,false,true,true,false,false}}),
		fixture.Select("SELECT ROW('a.b'::ltree,1)<ROW('a-b'::ltree,0) AS lt,ROW('a.b'::ltree,1)=ROW('a.b'::ltree,1) AS eq", []string{"lt","eq"}, []string{"BOOL","BOOL"}, [][]any{{true,true}}),
		ltreeComparisonGroupStep(b),
	}
}

func ltreeComparisonGroupStep(b fixture.Backend) fixture.Step {
	name := "LTREE"
	if b.PG { name = "" }
	s := fixture.Select("SELECT v,count(*) AS n FROM lt_cmp GROUP BY v ORDER BY v", []string{"v","n"}, []string{name,"INT8"}, [][]any{{"",int64(1)},{"a",int64(1)},{"a.b",int64(2)},{"a-b",int64(1)},{nil,int64(1)}})
	custom := ltreeSelect(b,"",[]string{"v"},nil).CheckRaw
	s.CheckRaw = func(t *testing.T,column,row int,value any) bool {
		if column != 0 { return false }
		return custom(t,0,row,value)
	}
	return s
}

func TestReferenceLtreeComparisonConsumers(t *testing.T) {
	ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		ltreeComparisonTable(t, p, b, c)
		for _, s := range ltreeComparisonConsumerChecks(b) {
			ltreeComparisonRun(t, p, b, c, s)
			ltreeComparisonRun(t, p, b, c, fixture.Prepared(s))
		}
		q := "SELECT id FROM lt_cmp WHERE v=$1::ltree ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, value := range []any{"a.b", []byte("a.b"), nil, ""} {
			s := ltreeComparisonIDs(q)
			s.Args = []any{value}
			if value == nil { // NULL is not an equality key.
			} else if text, ok := value.(string); ok && text == "" {
				s = ltreeComparisonIDs(q, 4); s.Args = []any{value}
			} else { s = ltreeComparisonIDs(q, 2, 6); s.Args = []any{value} }
			ltreeComparisonRun(t, p, b, held, s)
		}
		ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE TABLE lt_cmp_order(id integer,v ltree)", 0))
		paths := []string{"", "-", "0", "A", "Top", "Top.A", "Top.A.z", "Top.A-", "Top.AA", "Top.B", "_", "a", "a.b", "a-b", "a0"}
		ids := make([]int64, len(paths))
		for i := len(paths)-1; i >= 0; i-- {
			ids[i] = int64(i+1)
			ltreeComparisonRun(t, p, b, c, fixture.Exec("INSERT INTO lt_cmp_order VALUES ($1,$2::ltree)", 1, i+1, paths[i]))
		}
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_order ORDER BY v,id", ids...))
	})
}
