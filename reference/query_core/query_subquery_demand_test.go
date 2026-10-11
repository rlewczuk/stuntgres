package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Each shape has its own fixture and subtest so oracle discovery continues
// after a mismatch. Expectations are independent, not learned from native.
func TestReferenceQuerySubqueryTargetDemand(t *testing.T) {
	for _, tc := range []struct {
		name, suffix     string
		failure, present bool
	}{
		{"plain", "", false, true},
		{"order", " ORDER BY id", false, true},
		{"order-target", " ORDER BY 1", false, true},
		{"limit", " LIMIT 1", false, true},
		{"order-limit", " ORDER BY id LIMIT 1", false, true},
		{"limit-null", " LIMIT NULL", false, true},
		{"limit-zero", " LIMIT 0", false, false},
		{"offset-zero", " OFFSET 0", true, false},
		{"offset-one", " OFFSET 1", true, false},
		{"order-limit-offset", " ORDER BY id LIMIT 1 OFFSET 1", true, false},
		{"offset-empty", " OFFSET 9", true, false},
		{"rowmark", " FOR UPDATE", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE demand_outer(id INT)", 0))
				run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2),(3)", 3))
				run(fixture.Exec("CREATE TABLE demand_bad(v INT)", 0))
				run(fixture.Exec("INSERT INTO demand_bad VALUES(1),(2)", 2))
				q := "SELECT EXISTS(SELECT (SELECT v FROM demand_bad) FROM demand_outer" + tc.suffix + ") AS present"
				run(fixture.Exec("CREATE VIEW demand_view AS "+q, 0))
				for _, sqlText := range []string{q, "SELECT * FROM demand_view"} {
					if !tc.failure {
						subqueryBoth(run, fixture.Select(sqlText, []string{"present"}, []string{"BOOL"}, [][]any{{tc.present}}))
						continue
					}
					subqueryQueryFailure(t, p, b, c, sqlText, subqueryCardinality())
					stmt, err := c.PrepareContext(p.Ctx, sqlText)
					if err != nil {
						t.Fatal("Prepare must succeed", err)
					}
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: sqlText}
					subqueryQueryFailure(t, p, b, held, sqlText, subqueryCardinality())
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(sqlText, subqueryCardinality())
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
			})
		})
	}
}

func TestReferenceQuerySubqueryAggregateTargetDemand(t *testing.T) {
	for _, shape := range []struct {
		name, child string
		failure     bool
	}{
		{"aggregate", "SELECT (SELECT v FROM demand_bad),count(*) FROM demand_outer", true},
		{"having", "SELECT (SELECT v FROM demand_bad) FROM demand_outer HAVING TRUE", true},
		{"aggregate-empty", "SELECT (SELECT v FROM demand_bad),count(*) FROM demand_outer WHERE FALSE", true},
		{"having-false", "SELECT (SELECT v FROM demand_bad) FROM demand_outer HAVING FALSE", false},
	} {
		t.Run(shape.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE demand_outer(id INT)", 0))
				run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2)", 2))
				run(fixture.Exec("CREATE TABLE demand_bad(v INT)", 0))
				run(fixture.Exec("INSERT INTO demand_bad VALUES(1),(2)", 2))
				q := "SELECT EXISTS(" + shape.child + ") AS present"
				if shape.failure {
					subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					subqueryQueryFailure(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, subqueryCardinality())
				} else {
					subqueryBoth(run, fixture.Select(q, []string{"present"}, []string{"BOOL"}, [][]any{{false}}))
				}
			})
		})
	}
}
