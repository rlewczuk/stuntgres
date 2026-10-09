package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRandomGroupedKeyDemand(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			counts := map[int64]int64{}
			for range 3 {
				counts[randomScalar(t, p, c, b, "SELECT random(0,2147483647)", "random", "INT4").(int64)]++
			}
			continuation := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			expected := make([][]any, 0, len(counts))
			for key, count := range counts {
				expected = append(expected, []any{key, count})
			}
			q := "SELECT random(0,2147483647),count(*) FROM t GROUP BY 1"
			stmt, e := c.PrepareContext(p.ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, executor := range []referenceExecutor{c, held} {
				for range 2 {
					randomReseed(t, p, c, b, .25)
					step := referenceSelect(q, []string{"random", "count"}, []string{"INT4", "INT8"}, expected)
					step.bag = true
					step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
						var key, count int64
						if err := rows.Scan(&key, &count); err != nil {
							t.Fatal("grouped int64 scan", err)
						}
						want, found := counts[key]
						if !found || count <= 0 || count != want {
							t.Fatal("grouped typed multiplicity", key, count, want)
						}
					}
					referenceRunStep(t, p.ctx, executor, b.recorder, step)
					if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != continuation {
						t.Fatal("projected key redrawn")
					}
				}
			}
		})
	}
}
func TestReferenceRandomPreflight(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
	p.step(t, referenceExec("CREATE TABLE empty(id INT)", 0))
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, .25)
			step := referenceImplicitError("SELECT random(CAST('oops' AS int4),1) FROM empty", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "20"})
			step.selectRows = true
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, c, b.recorder, step)
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != first {
				t.Fatal("preflight consumed parent")
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM t WHERE id=1", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}))
			q := "CREATE TABLE defaults(v INT DEFAULT random(NULL,1))"
			if b.pg {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO defaults DEFAULT VALUES", 1))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM defaults", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			} else {
				if _, e := c.ExecContext(p.ctx, q); e == nil {
					t.Fatal("DEFAULT exclusion bypassed")
				}
			}
		})
	}
}
