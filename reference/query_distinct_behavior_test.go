package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctBehavior(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctSetup(t, run)
		all := [][]any{{int64(1), int64(1), int64(10)}, {int64(2), int64(1), int64(10)}, {int64(3), int64(1), int64(20)}, {int64(4), nil, int64(10)}, {int64(5), nil, int64(10)}, {int64(6), nil, nil}, {int64(7), nil, nil}}
		for _, s := range []referenceStep{
			referenceSelect("SELECT DISTINCT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}, {nil, int64(10)}, {nil, nil}}),
			referenceSelect("SELECT DISTINCT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}),
			referenceSelect("SELECT DISTINCT id,a,b FROM t", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all),
			referenceSelect("SELECT DISTINCT * FROM t", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all),
			referenceSelect("SELECT DISTINCT a IS NULL AS n FROM t", []string{"n"}, []string{"BOOL"}, [][]any{{false}, {true}}),
			referenceSelect("SELECT DISTINCT NULL,NULL FROM t", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}),
			referenceSelect("SELECT DISTINCT NULL,NULL FROM t WHERE FALSE", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{}),
			referenceSelect("SELECT DISTINCT a+1 AS x,b/10 AS y FROM t", []string{"x", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(1)}, {nil, nil}}),
			referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(1), int64(20)}, {nil, int64(10)}, {nil, int64(10)}, {nil, nil}, {nil, nil}}),
			referenceSelect("SELECT DISTINCT 1 AS one FROM t", []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}}),
		} {
			s.bag = true
			queryDistinctBoth(run, s)
		}
		run(referenceExec("DELETE FROM t", 7))
		s := queryDistinctOrdered()
		s.rows = [][]any{}
		queryDistinctBoth(run, s)
	})
}

func TestReferenceQueryDistinctOrdering(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctSetup(t, run)
		queryDistinctBoth(run, queryDistinctOrdered())
		for _, s := range []referenceStep{
			referenceSelect("SELECT DISTINCT a,b FROM t ORDER BY 1,2", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}, {nil, int64(10)}, {nil, nil}}),
			referenceSelect("SELECT DISTINCT a AS b FROM t ORDER BY b", []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}),
			referenceSelect("SELECT DISTINCT a+1 AS x FROM t ORDER BY a+1,x", []string{"x"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}}),
			referenceSelect("SELECT DISTINCT a AS x,a AS x FROM t ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {nil, nil}}),
			referenceSelect("SELECT DISTINCT a FROM t ORDER BY a LIMIT 1 OFFSET 1", []string{"a"}, []string{"INT4"}, [][]any{{nil}}),
			referenceSelect("SELECT DISTINCT 1/(id-2) AS q FROM t LIMIT 0", []string{"q"}, []string{"INT4"}, [][]any{}),
			referenceSelect("SELECT DISTINCT a,count(*) AS n FROM t GROUP BY a HAVING count(*)>1 ORDER BY a", []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(3)}, {nil, int64(4)}}),
		} {
			queryDistinctBoth(run, s)
		}
		run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
		q := "SELECT DISTINCT nextval('s') AS n FROM t ORDER BY n,n DESC"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for i := range 2 {
			want := [][]any{}
			for n := int64(1 + i*8); n <= int64(7+i*8); n++ {
				want = append(want, []any{n})
			}
			s := referenceSelect(q, []string{"n"}, []string{"INT8"}, want)
			if i == 0 {
				run(s)
			} else {
				referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
			}
			var next int64
			if err := c.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != int64(8+i*8) {
				t.Fatal(next, err)
			}
		}
		run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
		run(referenceExec("CREATE TABLE volatile_dest(n BIGINT)", 0))
		queryDistinctBoth(run, referenceExec("INSERT INTO volatile_dest SELECT DISTINCT nextval('s') AS n FROM t ORDER BY n,n DESC", 7))
		var next int64
		if err := c.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 15 {
			t.Fatal(next, err)
		}
	})
}
