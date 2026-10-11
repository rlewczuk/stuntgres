package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceMergedProducer(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			schema := fmt.Sprintf("sequence_merged_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = c.ExecContext(p.Ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.Ctx, "SET search_path TO public")
				_, _ = c.ExecContext(p.Ctx, "DROP SCHEMA "+schema+" CASCADE")
			})
			bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			steps := []fixture.Step{
				{Query: "CREATE SCHEMA " + schema},
				{Query: "SET search_path TO " + schema},
				{Query: "CREATE SEQUENCE s"},
				{Query: "CREATE TABLE t(v BIGINT)"},
				{Query: "INSERT INTO t SELECT nextval('s'::regclass)::bigint", Prepared: true, Affected: 1},
				{Query: "INSERT INTO t SELECT last_value FROM s", Prepared: true, Affected: 1},
				{Query: "INSERT INTO t VALUES(nextval(CAST('s' AS regclass))) ORDER BY 1", Prepared: true, Affected: 1},
				{Query: "SELECT v FROM t ORDER BY v", SelectRows: true, Columns: []string{"v"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(2)}}}),
				{Query: "SELECT num_nulls(NULL), nextval('s')::bigint", SelectRows: true, Columns: []string{"num_nulls", "nextval"}, Types: []string{"INT4", "INT8"}, Rows: [][]any{{int64(1), int64(3)}}},
				{SelectRows: true, Query: "SELECT setval('s',$1::text::bigint) FROM t WHERE v=99", Args: []any{"bad"}, State: "22P02", Diagnostic: &bad, ErrorStage: "execute"},
				{Query: "SELECT setval('s',$1::text::bigint) FROM t WHERE v=99", Args: []any{"bad"}, State: "22P02", Diagnostic: &bad, ErrorStage: "execute"},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(3)}}}),
				{Query: "BEGIN"},
				{Query: "CREATE SEQUENCE private_s START 40"},
				{Query: "INSERT INTO t SELECT last_value FROM private_s", Affected: 1},
				{Query: "ROLLBACK"},
				{Query: "SELECT v FROM t ORDER BY v", SelectRows: true, Columns: []string{"v"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
				{Query: "CREATE TABLE src(id INT, day DATE, payload BYTEA, nullable BIGINT)"},
				{Query: "INSERT INTO src VALUES(1,DATE '2024-02-29','\\x01',NULL),(2,DATE '2024-03-01','\\x02',7)", Affected: 2},
				{Query: "CREATE TABLE dst(id BIGINT, n BIGINT, day DATE, payload BYTEA, nullable BIGINT)"},
				{Query: "INSERT INTO dst SELECT id::bigint,nextval('s'),day,payload,nullable FROM src ORDER BY id", Prepared: true, Affected: 2},
				{Query: "SELECT * FROM dst ORDER BY id", SelectRows: true, Columns: []string{"id", "n", "day", "payload", "nullable"}, Types: []string{"INT8", "INT8", "DATE", "BYTEA", "INT8"}, Rows: [][]any{{int64(1), int64(4), "2024-02-29", "\x01", nil}, {int64(2), int64(5), "2024-03-01", "\x02", int64(7)}}},
				{Query: "INSERT INTO dst(id,n) SELECT id::bigint,nextval('s') FROM src WHERE id=99"},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(5)}}}),
				{Query: "CREATE TABLE bad(v TEXT)"},
				{Query: "INSERT INTO bad VALUES('bad')", Affected: 1},
				{Query: "INSERT INTO dst(id,n) SELECT nextval('s'),v::bigint FROM bad", State: "22P02", Diagnostic: &bad, ErrorStage: "execute"},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(6)}}}),
				{Query: "SELECT id FROM dst ORDER BY id", SelectRows: true, Columns: []string{"id"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}, {int64(2)}}},
				{Query: "SELECT num_nulls(nextval('s'))", SelectRows: true, Columns: []string{"num_nulls"}, Types: []string{"INT4"}, Rows: [][]any{{int64(0)}}},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(7)}}}),
				{Query: "SELECT 0 IS DISTINCT FROM nextval('s') FROM src WHERE id=1", SelectRows: true, Columns: []string{"?column?"}, Types: []string{"BOOL"}, Rows: [][]any{{true}}},
				fixture.SequenceInt64Step(fixture.Step{Query: "SELECT currval('s')", SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(8)}}}),
			}
			for _, step := range steps {
				if len(step.Types) == 5 && backend.PG {
					step.Rows = [][]any{{int64(1), int64(4), time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), "\x01", nil}, {int64(2), int64(5), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), "\x02", int64(7)}}
				}
				if step.Query == "INSERT INTO dst SELECT id::bigint,nextval('s'),day,payload,nullable FROM src ORDER BY id" {
					stmt, err := c.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(3)}})))
					step.Prepared = false
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: step.Query}, backend.Recorder, step)
				} else {
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
				}
			}
		})
	}
}
