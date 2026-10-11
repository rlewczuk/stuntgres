package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctOnConsumers(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(k) id,row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC) AS rn,count(*) OVER(PARTITION BY k) AS n FROM events ORDER BY k,stamp DESC,id DESC", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(3), int64(1), int64(3)}, {int64(5), int64(1), int64(2)}, {int64(7), int64(1), int64(2)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC)) id FROM events ORDER BY row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC),id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(1)}}))
		run(fixture.Exec("CREATE TABLE grouped(id INT,a INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO grouped VALUES(1,1,5),(2,1,5),(3,2,10),(4,3,30)", 4))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(sum(v)) a,sum(v) AS total FROM grouped GROUP BY a HAVING sum(v)>=10 ORDER BY sum(v),a DESC", []string{"a", "total"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(10)}, {int64(3), int64(30)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(sum(v)) 17 AS payload FROM grouped", []string{"payload"}, []string{"INT4"}, [][]any{{int64(17)}}))
		run(fixture.Exec("CREATE TABLE peers(id INT,a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO peers VALUES(1,1,10),(2,1,10),(3,1,20),(4,2,30)", 4))
		// The user ORDER contains a only: FETCH peers must not gain omitted b.
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT a,b FROM (SELECT DISTINCT ON(a,b) a,b FROM peers ORDER BY a FETCH FIRST 1 ROW WITH TIES) AS q ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(a,b) id FROM peers ORDER BY a,b,id DESC FETCH FIRST 1 ROW WITH TIES", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT id,payload FROM (SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC) AS q ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, queryDistinctOnHidden().Rows))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT (SELECT DISTINCT ON(k) id FROM events WHERE k=1 ORDER BY k,stamp DESC,id DESC) AS latest", []string{"latest"}, []string{"INT4"}, [][]any{{int64(3)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT EXISTS(SELECT DISTINCT ON(k) id FROM events ORDER BY k,stamp DESC,id DESC) AS present", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.QueryDistinctBoth(run, fixture.Select("(SELECT DISTINCT ON(k) id FROM events WHERE k=1 ORDER BY k,id DESC LIMIT 1) UNION ALL (SELECT DISTINCT ON(k) id FROM events WHERE k=2 ORDER BY k,id DESC LIMIT 1)", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}}))
		run(fixture.Exec("CREATE TABLE key_source(k INT)", 0))
		run(fixture.Exec("INSERT INTO key_source VALUES(1)", 1))
		run(fixture.Exec("CREATE VIEW on_subquery AS SELECT DISTINCT ON((SELECT k FROM key_source)) id FROM events ORDER BY (SELECT k FROM key_source),id DESC", 0))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT id FROM on_subquery", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("CREATE TABLE only_key(k INT)", 0))
		run(fixture.Exec("INSERT INTO only_key VALUES(1)", 1))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON((SELECT k FROM only_key)) id FROM events ORDER BY (SELECT only_key.k FROM only_key),id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|(SELECT q.k FROM only_key AS q)) e.id FROM events AS e ORDER BY (SELECT r.k FROM only_key AS r),e.id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
		q, want = queryDistinctOnMarked("SELECT DISTINCT ON(|(SELECT q.k FROM only_key AS q)) e.id FROM events AS e ORDER BY (SELECT e.k FROM only_key AS q),e.id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
	})
}

func TestReferenceQueryDistinctOnInsert(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE source(id INT,n NUMERIC)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1,1.1),(2,1.1),(3,1.2),(4,1.2)", 4))
		run(fixture.Exec("CREATE TABLE dest(id INT GENERATED ALWAYS AS IDENTITY,n INT)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO dest(id,n) OVERRIDING USER VALUE SELECT DISTINCT ON(n) id,n FROM source ORDER BY n,id DESC", 2)
			s.Prepared = prepared
			run(s)
		}
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT id,n FROM dest ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {int64(4), int64(1)}}))
		run(fixture.Exec("CREATE TABLE atomic_dest(n INT)", 0))
		run(fixture.Exec("INSERT INTO atomic_dest VALUES(17)", 1))
		q := "INSERT INTO atomic_dest SELECT DISTINCT ON(n) 1/(id-4) FROM source ORDER BY n,id DESC"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, route := range []string{"direct", "prepared", "held"} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.ErrorStage = "execute"
			switch route {
			case "direct":
				run(s)
			case "prepared":
				run(fixture.Prepared(s))
			default:
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
			}
			run(fixture.Select("SELECT n FROM atomic_dest", []string{"n"}, []string{"INT4"}, [][]any{{int64(17)}}))
		}
		run(fixture.Exec("DELETE FROM source WHERE id=4", 1))
		s := fixture.Exec(q, 2)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
		run(fixture.Select("SELECT n FROM atomic_dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(0)}, {int64(17)}}))
	})
}
