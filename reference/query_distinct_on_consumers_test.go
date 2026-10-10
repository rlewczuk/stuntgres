package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctOnConsumers(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(k) id,row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC) AS rn,count(*) OVER(PARTITION BY k) AS n FROM events ORDER BY k,stamp DESC,id DESC", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(3), int64(1), int64(3)}, {int64(5), int64(1), int64(2)}, {int64(7), int64(1), int64(2)}}))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC)) id FROM events ORDER BY row_number() OVER(PARTITION BY k ORDER BY stamp DESC,id DESC),id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(1)}}))
		run(referenceExec("CREATE TABLE grouped(id INT,a INT,v INT)", 0))
		run(referenceExec("INSERT INTO grouped VALUES(1,1,5),(2,1,5),(3,2,10),(4,3,30)", 4))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(sum(v)) a,sum(v) AS total FROM grouped GROUP BY a HAVING sum(v)>=10 ORDER BY sum(v),a DESC", []string{"a", "total"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(10)}, {int64(3), int64(30)}}))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(sum(v)) 17 AS payload FROM grouped", []string{"payload"}, []string{"INT4"}, [][]any{{int64(17)}}))
		run(referenceExec("CREATE TABLE peers(id INT,a INT,b INT)", 0))
		run(referenceExec("INSERT INTO peers VALUES(1,1,10),(2,1,10),(3,1,20),(4,2,30)", 4))
		// The user ORDER contains a only: FETCH peers must not gain omitted b.
		queryDistinctBoth(run, referenceSelect("SELECT a,b FROM (SELECT DISTINCT ON(a,b) a,b FROM peers ORDER BY a FETCH FIRST 1 ROW WITH TIES) AS q ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(a,b) id FROM peers ORDER BY a,b,id DESC FETCH FIRST 1 ROW WITH TIES", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		queryDistinctBoth(run, referenceSelect("SELECT id,payload FROM (SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC) AS q ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, queryDistinctOnHidden().rows))
		queryDistinctBoth(run, referenceSelect("SELECT (SELECT DISTINCT ON(k) id FROM events WHERE k=1 ORDER BY k,stamp DESC,id DESC) AS latest", []string{"latest"}, []string{"INT4"}, [][]any{{int64(3)}}))
		queryDistinctBoth(run, referenceSelect("SELECT EXISTS(SELECT DISTINCT ON(k) id FROM events ORDER BY k,stamp DESC,id DESC) AS present", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		queryDistinctBoth(run, referenceSelect("(SELECT DISTINCT ON(k) id FROM events WHERE k=1 ORDER BY k,id DESC LIMIT 1) UNION ALL (SELECT DISTINCT ON(k) id FROM events WHERE k=2 ORDER BY k,id DESC LIMIT 1)", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}}))
		run(referenceExec("CREATE TABLE key_source(k INT)", 0))
		run(referenceExec("INSERT INTO key_source VALUES(1)", 1))
		run(referenceExec("CREATE VIEW on_subquery AS SELECT DISTINCT ON((SELECT k FROM key_source)) id FROM events ORDER BY (SELECT k FROM key_source),id DESC", 0))
		queryDistinctBoth(run, referenceSelect("SELECT id FROM on_subquery", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("CREATE TABLE only_key(k INT)", 0))
		run(referenceExec("INSERT INTO only_key VALUES(1)", 1))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON((SELECT k FROM only_key)) id FROM events ORDER BY (SELECT only_key.k FROM only_key),id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|(SELECT q.k FROM only_key AS q)) e.id FROM events AS e ORDER BY (SELECT r.k FROM only_key AS r),e.id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		queryDistinctAnalysis(t, p, b, c, q, want)
		q, want = queryDistinctOnMarked("SELECT DISTINCT ON(|(SELECT q.k FROM only_key AS q)) e.id FROM events AS e ORDER BY (SELECT e.k FROM only_key AS q),e.id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		queryDistinctAnalysis(t, p, b, c, q, want)
	})
}

func TestReferenceQueryDistinctOnInsert(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE source(id INT,n NUMERIC)", 0))
		run(referenceExec("INSERT INTO source VALUES(1,1.1),(2,1.1),(3,1.2),(4,1.2)", 4))
		run(referenceExec("CREATE TABLE dest(id INT GENERATED ALWAYS AS IDENTITY,n INT)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO dest(id,n) OVERRIDING USER VALUE SELECT DISTINCT ON(n) id,n FROM source ORDER BY n,id DESC", 2)
			s.prepared = prepared
			run(s)
		}
		queryDistinctBoth(run, referenceSelect("SELECT id,n FROM dest ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {int64(4), int64(1)}}))
		run(referenceExec("CREATE TABLE atomic_dest(n INT)", 0))
		run(referenceExec("INSERT INTO atomic_dest VALUES(17)", 1))
		q := "INSERT INTO atomic_dest SELECT DISTINCT ON(n) 1/(id-4) FROM source ORDER BY n,id DESC"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, route := range []string{"direct", "prepared", "held"} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			s.errorStage = "execute"
			switch route {
			case "direct":
				run(s)
			case "prepared":
				run(referencePrepared(s))
			default:
				referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
			}
			run(referenceSelect("SELECT n FROM atomic_dest", []string{"n"}, []string{"INT4"}, [][]any{{int64(17)}}))
		}
		run(referenceExec("DELETE FROM source WHERE id=4", 1))
		s := referenceExec(q, 2)
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
		run(referenceSelect("SELECT n FROM atomic_dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(0)}, {int64(17)}}))
	})
}
