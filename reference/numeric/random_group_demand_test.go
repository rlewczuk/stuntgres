package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomGroupedKeyDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2),(3)", 3))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
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
			stmt, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, executor := range []fixture.Executor{c, held} {
				for range 2 {
					randomReseed(t, p, c, b, .25)
					step := fixture.Select(q, []string{"random", "count"}, []string{"INT4", "INT8"}, expected)
					step.Bag = true
					step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
						var key, count int64
						if err := rows.Scan(&key, &count); err != nil {
							t.Fatal("grouped int64 scan", err)
						}
						want, found := counts[key]
						if !found || count <= 0 || count != want {
							t.Fatal("grouped typed multiplicity", key, count, want)
						}
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != continuation {
						t.Fatal("projected key redrawn")
					}
				}
			}
		})
	}
}
func TestReferenceRandomPreflight(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2),(3)", 3))
	p.Step(t, fixture.Exec("CREATE TABLE empty(id INT)", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, .25)
			step := fixture.ImplicitError("SELECT random(CAST('oops' AS int4),1) FROM empty", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "20"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != first {
				t.Fatal("preflight consumed parent")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM t WHERE id=1", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}))
			q := "CREATE TABLE defaults(v INT DEFAULT random(NULL,1))"
			if b.PG {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO defaults DEFAULT VALUES", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM defaults", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			} else {
				if _, e := c.ExecContext(p.Ctx, q); e == nil {
					t.Fatal("DEFAULT exclusion bypassed")
				}
			}
		})
	}
}
