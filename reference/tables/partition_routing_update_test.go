package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingUpdate(t *testing.T) {
	for _, mode := range []string{"direct", "held"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE pr_move(id INT,k INT,visits INT) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pr_low PARTITION OF pr_move FOR VALUES FROM(1) TO(3)", 0))
					run(fixture.Exec("CREATE TABLE pr_high PARTITION OF pr_move FOR VALUES FROM(3) TO(5)", 0))
					run(fixture.Exec("INSERT INTO pr_move VALUES(10,2,0),(20,3,0)", 2))

					updateSQL := "UPDATE pr_move SET k=k+$1,visits=visits+$2 WHERE id >= $3"
					readSQL := "SELECT id,k,visits FROM pr_move ORDER BY id"
					write, read := run, run
					if mode == "held" {
						update, err := backend.DB.PrepareContext(p.Ctx, updateSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer update.Close()
						selectStmt, err := backend.DB.PrepareContext(p.Ctx, readSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer selectStmt.Close()
						writer := fixture.HeldStatement{Executor: backend.DB, Statement: update, SQL: updateSQL}
						reader := fixture.HeldStatement{Executor: backend.DB, Statement: selectStmt, SQL: readSQL}
						write = func(s fixture.Step) {
							fixture.RunStep(t, p.Ctx, writer, backend.Recorder, s)
						}
						read = func(s fixture.Step) {
							fixture.RunStep(t, p.Ctx, reader, backend.Recorder, s)
						}
					}
					rows := func(query string, values [][]any) fixture.Step {
						s := fixture.Select(query, []string{"id", "k", "visits"}, []string{"INT4", "INT4", "INT4"}, values)
						s.CheckMetadata = referenceRangeMetadata(backend.PG, s.Columns, s.Types, []bool{true, true, true})
						return s
					}
					assertPlacement := func() {
						want := [][]any{{int64(10), int64(3), int64(1)}, {int64(20), int64(4), int64(1)}}
						read(rows(readSQL, want))
						run(rows("SELECT id,k,visits FROM pr_low ORDER BY id", nil))
						run(rows("SELECT id,k,visits FROM pr_high ORDER BY id", want))
					}

					// Both rows qualify in the captured source. Moving id=10 into the
					// other candidate's leaf must not visit it again or increment twice.
					write(fixture.Exec(updateSQL, 2, 1, 1, 0))
					assertPlacement()
					// Reuse the same prepared handles after movement, including an
					// unchanged-value UPDATE and a zero-match execution.
					write(fixture.Exec(updateSQL, 2, 0, 0, 0))
					assertPlacement()
					write(fixture.Exec(updateSQL, 0, 0, 0, 99))
					assertPlacement()
				})
			}
		})
	}
}
