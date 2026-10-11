package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func cteReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func cteBag(q string, columns, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, columns, types, rows, args...)
	s.Bag = true
	return s
}

func cteFixture(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,NULL),(2,NULL)", 3))
}

func TestReferenceQueryCTEDependencies(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cteFixture(run)
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			subqueryBoth(run, cteBag("WITH a(k,v) AS "+hint+" (SELECT id,v FROM t), b AS (SELECT k,v FROM a WHERE k=2) SELECT * FROM b", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), nil}, {int64(2), nil}}))
		}
		for _, s := range []fixture.Step{
			cteBag("WITH c(k) AS (SELECT id,v FROM t) SELECT * FROM c", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}),
			cteBag("WITH c AS (SELECT id,v FROM t) SELECT a.x,b.id FROM c a(x) JOIN c b ON a.x=b.id", []string{"x", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}}),
			cteBag("WITH c AS (SELECT 'hello' AS s,NULL AS n) SELECT * FROM c", []string{"s", "n"}, []string{"TEXT", "TEXT"}, [][]any{{"hello", nil}}),
			cteBag("WITH c AS (SELECT id,v FROM t WHERE FALSE) SELECT * FROM c", []string{"id", "v"}, []string{"INT4", "INT4"}, nil),
			cteBag("WITH c(x,x) AS (SELECT 1,2) SELECT * FROM c", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}),
			cteBag("WITH c AS (SELECT FROM t) SELECT FROM c", []string{}, []string{}, [][]any{{}, {}, {}}),
		} {
			subqueryBoth(run, s)
		}
	})
}

func TestReferenceQueryCTEReuse(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cteFixture(run)
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			subqueryBoth(run, cteBag("WITH c AS "+hint+" (SELECT id FROM t) SELECT a.id,b.id FROM c a JOIN c b ON a.id=b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
		}
	})
}

func TestReferenceQueryCTEScope(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE c(n INT)", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		for _, tc := range []struct {
			q     string
			names []string
			rows  [][]any
		}{
			{"WITH c AS (SELECT 1 AS n) SELECT c.n,p.n FROM c CROSS JOIN public.c p", []string{"n", "n"}, [][]any{{int64(1), int64(9)}}},
			{"WITH c AS (SELECT n FROM c) SELECT n FROM c", []string{"n"}, [][]any{{int64(9)}}},
			{"WITH c AS (SELECT 1 AS n) SELECT d.n,c.n FROM (WITH c AS (SELECT n+1 AS n FROM c) SELECT n FROM c) d CROSS JOIN c", []string{"n", "n"}, [][]any{{int64(2), int64(1)}}},
			{"WITH a AS (SELECT 1 AS n), b AS (SELECT n+1 AS n FROM a) SELECT n FROM b UNION ALL SELECT n FROM a", []string{"n"}, [][]any{{int64(2)}, {int64(1)}}},
			{"WITH \"C\" AS (SELECT 3 AS n), c AS (SELECT 4 AS n) SELECT a.n,b.n FROM \"C\" a CROSS JOIN C b", []string{"n", "n"}, [][]any{{int64(3), int64(4)}}},
			{"WITH \"é\" AS (SELECT 5 AS n) SELECT n FROM \"é\"", []string{"n"}, [][]any{{int64(5)}}},
			{"WITH c AS (SELECT 1 AS n) SELECT n FROM (WITH a AS (SELECT n FROM c), c AS (SELECT 2 AS n) SELECT n FROM a) d", []string{"n"}, [][]any{{int64(1)}}},
		} {
			types := make([]string, len(tc.names))
			for i := range types {
				types[i] = "INT4"
			}
			subqueryBoth(run, cteBag(tc.q, tc.names, types, tc.rows))
		}
		subqueryBoth(run, cteBag("SELECT n FROM c", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}

func TestReferenceQueryCTEParenthesized(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cteFixture(run)
		// PostgreSQL attaches decorations to the same Select through parentheses:
		// a second WITH is an error, not another lexical query boundary.
		want := fixture.ControlsReferenceDiagnostic("42601", "multiple WITH clauses not allowed")
		want.Position = "1"
		for _, q := range []string{
			"WITH c AS (SELECT 1 AS n) (WITH c AS (SELECT 2 AS n) SELECT n FROM c)",
			"WITH c AS (SELECT 1 AS n) (WITH c AS (SELECT 2 AS n) SELECT c.n FROM c) ORDER BY c.n",
			"WITH c AS (SELECT 1 AS n) (WITH c AS (SELECT 2 AS n) SELECT t.id,c.n FROM t CROSS JOIN c) ORDER BY t.id FOR UPDATE OF t",
			"WITH a AS (SELECT 1 AS n) (WITH b AS (SELECT n+1 AS n FROM a) SELECT n FROM b)",
		} {
			subqueryAnalysisFailure(run, q, want)
		}
		// A single WITH through parentheses retains its original namespace.
		subqueryBoth(run, cteBag("WITH c AS (SELECT 2 AS n) (SELECT c.n FROM c) ORDER BY c.n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		subqueryBoth(run, fixture.Select("WITH c AS (SELECT 2 AS n) (SELECT t.id,c.n FROM t CROSS JOIN c) ORDER BY t.id FOR UPDATE OF t", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
	})
}

func cteExecutionFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic) {
	t.Helper()
	subqueryQueryFailure(t, p, b, c, q, want)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("execution error must not fail analysis", err)
	}
	defer stmt.Close()
	subqueryQueryFailure(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want)
}
