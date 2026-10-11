package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExprCollationConsumers(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		// PostgreSQL's hashable uncorrelated subplan demands hashing; correlated
		// membership demands comparison. CASE test provenance is implicit.
		cases := []fixture.Step{
			exprCollationDemand(`SELECT a IS DISTINCT FROM b AS v FROM expr_coll_t WHERE id=1`),
			exprCollationDemand(`SELECT a IS NOT DISTINCT FROM b AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT a IS DISTINCT FROM b AS v,a IS NOT DISTINCT FROM b AS w FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"v", "w"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {true, false}, {false, true}}),
			exprCollationDemand(`SELECT CASE a WHEN b THEN 1 ELSE 0 END AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT CASE a WHEN b THEN 1 ELSE 0 END AS v FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}}),
			exprCollationDemand(`SELECT NULLIF(a,b) AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT NULLIF(a,b) AS v FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {"A"}, {nil}}),
			exprCollationDemand(`SELECT GREATEST(a,b) AS v FROM expr_coll_t WHERE id=1`),
			exprCollationDemand(`SELECT LEAST(a,b) AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT GREATEST(a,b) AS v,LEAST(a,b) AS w FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"v", "w"}, []string{"TEXT", "TEXT"}, [][]any{{"A", "A"}, {"A", "A"}, {nil, nil}}),
			fixture.Select(`SELECT min(a||b) AS v,max(a||b) AS w FROM expr_coll_t WHERE FALSE`, []string{"v", "w"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}),
			fixture.Select(`SELECT min(a||b) AS v,max(a||b) AS w FROM expr_coll_t WHERE id>=3`, []string{"v", "w"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}),
			fixture.Select(`SELECT min(a||b) AS v,max(a||b) AS w FROM expr_coll_t WHERE id=1`, []string{"v", "w"}, []string{"TEXT", "TEXT"}, [][]any{{"AA", "AA"}}),
			exprCollationDemand(`SELECT min(a||b) AS v FROM expr_coll_t WHERE id<=2`),
			exprCollationDemand(`SELECT max(a||b) AS v FROM expr_coll_t WHERE id<=2`),
			exprCollationDemand(`SELECT l.id FROM expr_coll_t l JOIN expr_coll_t r ON l.a=r.b WHERE l.id=1 AND r.id=1`),
			fixture.Select(`SELECT l.id FROM expr_coll_t l JOIN expr_coll_t r ON l.a COLLATE "C"=r.b WHERE l.id=1 AND r.id=1`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		}
		for _, op := range []string{"IN", "= ANY", "= ALL"} {
			demand := exprCollationDemand(`SELECT a ` + op + ` (SELECT b FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id=1`)
			if op != "= ALL" {
				demand.Diagnostic.Message = "could not determine which collation to use for string hashing"
			}
			demand.ErrorStage = "execute"
			cases = append(cases, demand)
			empty := false
			if op == "= ALL" {
				empty = true
			}
			cases = append(cases, fixture.Select(`SELECT a `+op+` (SELECT b FROM expr_coll_t WHERE FALSE) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{empty}}),
				fixture.Select(`SELECT a `+op+` (SELECT b FROM expr_coll_t WHERE id=4) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			cases = append(cases,
				fixture.Select(`SELECT a COLLATE "C" `+op+` (SELECT b FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
				fixture.Select(`SELECT a COLLATE "C" `+op+` (SELECT b FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id=2`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
			if op != "= ALL" {
				// Correlation prevents a hashed subplan: actual reached equality
				// must demand comparison, not invent a string-hashing preflight.
				cases = append(cases,
					exprCollationDemand(`SELECT o.a `+op+` (SELECT i.b FROM expr_coll_t i WHERE i.id=o.id) AS v FROM expr_coll_t o WHERE o.id=1`),
					fixture.Select(`SELECT o.a `+op+` (SELECT i.b FROM expr_coll_t i WHERE i.id=o.id AND FALSE) AS v FROM expr_coll_t o WHERE o.id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
					fixture.Select(`SELECT o.a `+op+` (SELECT i.b FROM expr_coll_t i WHERE i.id=o.id+3) AS v FROM expr_coll_t o WHERE o.id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}),
					fixture.Select(`SELECT o.a COLLATE "C" `+op+` (SELECT i.b FROM expr_coll_t i WHERE i.id=o.id) AS v FROM expr_coll_t o WHERE o.id<=2 ORDER BY o.id`, []string{"v"}, []string{"BOOL"}, [][]any{{true}, {false}}))
			}
		}
		cases = append(cases,
			fixture.Select(`SELECT (id,a)<(id+1,b) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationDemand(`SELECT (id,a)<(id,b) AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT (id,a)=(id+1,b) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
			exprCollationDemand(`SELECT (id,a)=(id,b) AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT a COLLATE "C" IS DISTINCT FROM b AS v,a COLLATE "C" IS NOT DISTINCT FROM b AS w FROM expr_coll_t WHERE id=1`, []string{"v", "w"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}),
			exprCollationDemand(`SELECT CASE a COLLATE "C" WHEN b THEN 1 ELSE 0 END AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT CASE a WHEN b COLLATE "C" THEN 1 ELSE 0 END AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
			// The test's explicit provenance is lost at CaseTestExpr; the
			// explicit WHEN annotation wins even when the identities differ.
			fixture.Select(`SELECT CASE a COLLATE "C" WHEN b COLLATE "POSIX" THEN 1 ELSE 0 END AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT NULLIF(a COLLATE "C",b) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
			exprCollationExplicit(`SELECT a COLLATE "C" IS DISTINCT FROM b COLLATE "POSIX" FROM expr_coll_t WHERE FALSE`, "C", "POSIX"),
			fixture.Select(`SELECT CASE a COLLATE "C" WHEN b COLLATE "POSIX" THEN 1 ELSE 0 END FROM expr_coll_t WHERE FALSE`, []string{"case"}, []string{"INT4"}, nil))
		exprCollationCases(t, p, b, cases)
		fixture.CollationExec(t, p, b, `CREATE TABLE expr_coll_left(k TEXT COLLATE "C")`, `CREATE TABLE expr_coll_right(k TEXT COLLATE "POSIX")`)
		exprCollationCases(t, p, b, []fixture.Step{
			exprCollationDemand(`SELECT k FROM expr_coll_left JOIN expr_coll_right USING(k)`),
			exprCollationDemand(`SELECT k FROM expr_coll_left NATURAL JOIN expr_coll_right`),
		})
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_left VALUES('A')`, 1))
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_right VALUES('A')`, 1))
		exprCollationCases(t, p, b, []fixture.Step{
			exprCollationDemand(`SELECT k FROM expr_coll_left JOIN expr_coll_right USING(k)`),
			exprCollationDemand(`SELECT k FROM expr_coll_left NATURAL JOIN expr_coll_right`),
		})
	})
}
