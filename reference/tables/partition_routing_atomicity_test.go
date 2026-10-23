package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingAtomicity(t *testing.T) {
	for _, kind := range []string{"missing", "check", "not_null", "unique"} {
		t.Run(kind, func(t *testing.T) {
			referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				referenceRoutingConstraintTree(run)
				seed := "INSERT INTO pr_constraint VALUES(10,2,10),(20,2,20),(30,3,30)"
				if kind == "missing" {
					seed = "INSERT INTO pr_constraint VALUES(10,2,10),(20,4,20),(30,3,30)"
				}
				run(fixture.Exec(seed, 3))
				query := "UPDATE pr_constraint SET k=3,payload=CASE WHEN id=20 THEN "
				proposed := "20, 3, "
				switch kind {
				case "check":
					query += "0"
					proposed += "0"
				case "not_null":
					query += "NULL"
					proposed += "null"
				case "unique":
					query += "30"
				case "missing":
					query = "UPDATE pr_constraint SET k=k+1"
				}
				if kind != "missing" {
					query += " ELSE payload END WHERE id<30"
				}
				query += " RETURNING id,k,payload"
				stmt, err := b.DB.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
				for _, executor := range []fixture.Executor{b.DB, held} {
					var failure fixture.Step
					if kind == "missing" {
						failure = referenceRangeMiss(query, "pr_constraint", "k", "5")
					} else {
						failure = referenceRoutingConstraintFailure(query, kind, proposed)
					}
					failure.SelectRows = true
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, failure)
					k20 := int64(2)
					if kind == "missing" {
						k20 = 4
					}
					low := [][]any{{int64(10), int64(2), int64(10)}, {int64(20), k20, int64(20)}}
					high := [][]any{{int64(30), int64(3), int64(30)}}
					if kind == "missing" {
						low = low[:1]
						high = [][]any{{int64(20), int64(4), int64(20)}, {int64(30), int64(3), int64(30)}}
					}
					run(fixture.Select("SELECT id,k,payload FROM pr_constraint_low ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, low))
					run(fixture.Select("SELECT id,k,payload FROM pr_constraint_high ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, high))
				}
				// A subsequent claim of the would-be destination key must succeed;
				// neither a destination prefix nor a failed index claim survives.
				run(fixture.Exec("INSERT INTO pr_constraint VALUES(40,3,10)", 1))
			})
		})
	}
}
