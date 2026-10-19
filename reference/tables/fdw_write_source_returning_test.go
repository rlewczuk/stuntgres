package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWriteCombinedSourceFrame(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader) {
	t.Helper()
	for _, command := range []string{"UPDATE", "DELETE"} {
		for _, prepared := range []bool{false, true} {
			name := command + "/direct"
			if prepared {
				name = command + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
				run(fixture.Exec("BEGIN", 0))
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Error("rolling back source-frame witness", err)
					}
				}()
				// Both duplicate matches project identical values: PostgreSQL may
				// choose either source row, but must mutate the target only once.
				q := "UPDATE fdw_data f SET id=11,label=m.tag FROM (VALUES(1,'joined'),(1,'joined')) AS m(k,tag) WHERE f.id=m.k RETURNING old.id AS before,new.id AS after,f.label AS actual,m.k AS source_id,m.tag AS source_tag"
				expected := [][]any{{int64(1), int64(11), "joined", int64(1), "joined"}}
				if command == "DELETE" {
					q = "DELETE FROM fdw_data f USING (VALUES(1,'joined'),(1,'joined')) AS m(k,tag) WHERE f.id=m.k RETURNING old.id AS before,new.id AS after,f.label AS actual,m.k AS source_id,m.tag AS source_tag"
					expected = [][]any{{int64(1), nil, "one", int64(1), "joined"}}
				}
				step := fixture.Select(q, []string{"before", "after", "actual", "source_id", "source_tag"}, []string{"INT4", "INT4", "TEXT", "INT4", "TEXT"}, expected)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				var staged [][]any
				if command == "UPDATE" {
					staged = [][]any{{int64(11), "joined", true}}
				}
				run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id IN(1,11)", staged))
			})
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id IN(1,11)", [][]any{{int64(1), "one", true}}))
		}
	}
}
