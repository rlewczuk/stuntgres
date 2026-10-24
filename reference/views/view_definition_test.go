package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Pin the physical transaction clock: no sleep or midnight-boundary assumption.
func TestReferenceViewFrozenRelativeLiteralOracle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("CREATE VIEW v(d) AS SELECT DATE 'today' FROM t", 0))
			var captured string
			if err := c.QueryRowContext(p.Ctx, "SELECT DATE 'today'::TEXT FROM t").Scan(&captured); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(`SET "DateStyle"='ISO, DMY'`, 0))
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(`SET "DateStyle"='ISO, MDY'`, 0))
			run(fixture.Exec("CREATE VIEW reconstructed AS "+definition, 0))
			for _, name := range []string{"v", "reconstructed"} {
				run(fixture.Select("SELECT d FROM "+name, []string{"d"}, []string{"DATE"}, [][]any{{fixture.DateExpected(b.PG, captured)}}))
			}
		})
	}
}

func TestReferenceViewContextualDateStyleOracle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec(`SET "DateStyle"='ISO, DMY'`, 0))
			run(fixture.Exec("CREATE TABLE t(id INT,d DATE,ts TIMESTAMP)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')", 2))
			run(fixture.Exec("CREATE VIEW v(id) AS SELECT id FROM t WHERE d='01/02/2000'", 0))
			run(fixture.Exec("CREATE VIEW w(matches) AS SELECT '01/02/2000' BETWEEN d AND ts FROM t WHERE id=1", 0))
			run(fixture.Exec(`SET "DateStyle"='ISO, MDY'`, 0))
			for _, name := range []string{"v", "w"} {
				var definition string
				if err := c.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE VIEW "+name+"_copy AS "+definition, 0))
			}
			for _, name := range []string{"v", "v_copy"} {
				run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
			for _, name := range []string{"w", "w_copy"} {
				run(fixture.Select("SELECT matches FROM "+name, []string{"matches"}, []string{"BOOL"}, [][]any{{true}}))
			}
		})
	}
}
