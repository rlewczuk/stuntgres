package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingTransactions(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "error", "savepoint"} {
		t.Run(outcome, func(t *testing.T) {
			referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pr_tx(id INT,k INT) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE pr_tx_low PARTITION OF pr_tx FOR VALUES FROM(1) TO(3)", 0))
				run(fixture.Exec("CREATE TABLE pr_tx_high PARTITION OF pr_tx FOR VALUES FROM(3) TO(5)", 0))
				run(fixture.Exec("INSERT INTO pr_tx VALUES(10,2),(20,4)", 2))
				rows := func(q string, values [][]any) fixture.Step {
					return fixture.Select(q, []string{"id", "k"}, []string{"INT4", "INT4"}, values)
				}
				original := [][]any{{int64(10), int64(2)}, {int64(20), int64(4)}}
				query := "SELECT id,k FROM pr_tx ORDER BY id"
				// An implicit late failure leaves a usable connection and all rows intact.
				run(referenceRangeMiss("UPDATE pr_tx SET k=k+1", "pr_tx", "k", "5"))
				run(rows(query, original))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				step(fixture.Exec("INSERT INTO pr_tx VALUES(30,2)", 1))
				moveSQL := "UPDATE pr_tx SET k=$1 WHERE id=$2"
				stmt, err := tx.PrepareContext(p.Ctx, moveSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: moveSQL}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(moveSQL, 1, 3, 30))
				step(rows(query, [][]any{{int64(10), int64(2)}, {int64(20), int64(4)}, {int64(30), int64(3)}}))
				run(rows(query, original))
				if outcome == "savepoint" {
					step(fixture.Exec("SAVEPOINT before_move", 0))
				}
				if outcome == "error" || outcome == "savepoint" {
					step(referenceRangeMiss("UPDATE pr_tx SET k=k+1", "pr_tx", "k", "5"))
					step(referenceRangeError("SELECT id,k FROM pr_tx", "25P02", "current transaction is aborted, commands ignored until end of transaction block", fixture.Diagnostic{}))
					if outcome == "savepoint" {
						step(fixture.Exec("ROLLBACK TO before_move", 0))
						step(rows(query, [][]any{{int64(10), int64(2)}, {int64(20), int64(4)}, {int64(30), int64(3)}}))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(moveSQL, 1, 3, 10))
					}
				}
				if outcome == "commit" || outcome == "savepoint" {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					k10 := int64(2)
					if outcome == "savepoint" {
						k10 = 3
					}
					run(rows(query, [][]any{{int64(10), k10}, {int64(20), int64(4)}, {int64(30), int64(3)}}))
					low := [][]any{{int64(10), int64(2)}}
					high := [][]any{{int64(20), int64(4)}, {int64(30), int64(3)}}
					if outcome == "savepoint" {
						low = nil
						high = [][]any{{int64(10), int64(3)}, {int64(20), int64(4)}, {int64(30), int64(3)}}
					}
					run(rows("SELECT id,k FROM pr_tx_low ORDER BY id", low))
					run(rows("SELECT id,k FROM pr_tx_high ORDER BY id", high))
				} else {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(rows(query, original))
					run(rows("SELECT id,k FROM pr_tx_low", original[:1]))
					run(rows("SELECT id,k FROM pr_tx_high", original[1:]))
				}
			})
		})
	}
}
