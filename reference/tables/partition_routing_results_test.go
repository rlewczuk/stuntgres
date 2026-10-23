package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingResults(t *testing.T) {
	referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pr_result(id INT NOT NULL,k INT NOT NULL,visits INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pr_result_low PARTITION OF pr_result FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE pr_result_high PARTITION OF pr_result FOR VALUES FROM(3) TO(5)", 0))
		run(fixture.Exec("INSERT INTO pr_result VALUES(10,2,0),(20,3,0)", 2))
		query := "UPDATE pr_result SET k=$1 WHERE id=$2 RETURNING id,k,visits"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		readSQL := "SELECT id,k,visits FROM pr_result ORDER BY id"
		reader, err := b.DB.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		read := fixture.HeldStatement{Executor: b.DB, Statement: reader, SQL: readSQL}
		result := func(q string, rows [][]any, args ...any) fixture.Step {
			s := fixture.Select(q, []string{"id", "k", "visits"}, []string{"INT4", "INT4", "INT4"}, rows, args...)
			s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{false, false, true})
			return s
		}
		for _, k := range []int64{3, 2, 4} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, result(query, [][]any{{int64(10), k, int64(0)}}, k, 10))
			fixture.RunStep(t, p.Ctx, read, b.Recorder, result(readSQL, [][]any{{int64(10), k, int64(0)}, {int64(20), int64(3), int64(0)}}))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, result(query, nil, 2, 99))
		// OLD remains the logical source row even when NEW belongs to another leaf.
		s := fixture.Select("UPDATE pr_result SET k=2,visits=visits+1 RETURNING id,old.k AS before,new.k AS after,visits", []string{"id", "before", "after", "visits"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(4), int64(2), int64(1)}, {int64(20), int64(3), int64(2), int64(1)}})
		s.Bag = true
		run(s)
		run(result("SELECT id,k,visits FROM pr_result_high", nil))
		run(result("SELECT id,k,visits FROM pr_result_low ORDER BY id", [][]any{{int64(10), int64(2), int64(1)}, {int64(20), int64(2), int64(1)}}))
		// Held Exec shares the same statement as held Query and checks both the
		// affected count and unsupported LastInsertId through the fixture.
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, 3, 10))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0, 3, 99))
		run(fixture.Exec("UPDATE pr_result SET visits=visits", 2))
		fixture.RunStep(t, p.Ctx, read, b.Recorder, result(readSQL, [][]any{{int64(10), int64(3), int64(1)}, {int64(20), int64(2), int64(1)}}))
	})
}
