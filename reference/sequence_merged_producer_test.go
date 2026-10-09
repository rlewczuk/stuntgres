package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceMergedProducer(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c := sequenceCoreConn(t, p.ctx, backend.db)
			schema := fmt.Sprintf("sequence_merged_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = c.ExecContext(p.ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.ctx, "SET search_path TO public")
				_, _ = c.ExecContext(p.ctx, "DROP SCHEMA "+schema+" CASCADE")
			})
			bad := controlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			steps := []referenceStep{
				{query: "CREATE SCHEMA " + schema},
				{query: "SET search_path TO " + schema},
				{query: "CREATE SEQUENCE s"},
				{query: "CREATE TABLE t(v BIGINT)"},
				{query: "INSERT INTO t SELECT nextval('s'::regclass)::bigint", prepared: true, affected: 1},
				{query: "INSERT INTO t SELECT last_value FROM s", prepared: true, affected: 1},
				{query: "INSERT INTO t VALUES(nextval(CAST('s' AS regclass))) ORDER BY 1", prepared: true, affected: 1},
				{query: "SELECT v FROM t ORDER BY v", selectRows: true, columns: []string{"v"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(2)}}}),
				{query: "SELECT num_nulls(NULL), nextval('s')::bigint", selectRows: true, columns: []string{"num_nulls", "nextval"}, types: []string{"INT4", "INT8"}, rows: [][]any{{int64(1), int64(3)}}},
				{selectRows: true, query: "SELECT setval('s',$1::text::bigint) FROM t WHERE v=99", args: []any{"bad"}, state: "22P02", diagnostic: &bad, errorStage: "execute"},
				{query: "SELECT setval('s',$1::text::bigint) FROM t WHERE v=99", args: []any{"bad"}, state: "22P02", diagnostic: &bad, errorStage: "execute"},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(3)}}}),
				{query: "BEGIN"},
				{query: "CREATE SEQUENCE private_s START 40"},
				{query: "INSERT INTO t SELECT last_value FROM private_s", affected: 1},
				{query: "ROLLBACK"},
				{query: "SELECT v FROM t ORDER BY v", selectRows: true, columns: []string{"v"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
				{query: "CREATE TABLE src(id INT, day DATE, payload BYTEA, nullable BIGINT)"},
				{query: "INSERT INTO src VALUES(1,DATE '2024-02-29','\\x01',NULL),(2,DATE '2024-03-01','\\x02',7)", affected: 2},
				{query: "CREATE TABLE dst(id BIGINT, n BIGINT, day DATE, payload BYTEA, nullable BIGINT)"},
				{query: "INSERT INTO dst SELECT id::bigint,nextval('s'),day,payload,nullable FROM src ORDER BY id", prepared: true, affected: 2},
				{query: "SELECT * FROM dst ORDER BY id", selectRows: true, columns: []string{"id", "n", "day", "payload", "nullable"}, types: []string{"INT8", "INT8", "DATE", "BYTEA", "INT8"}, rows: [][]any{{int64(1), int64(4), "2024-02-29", "\x01", nil}, {int64(2), int64(5), "2024-03-01", "\x02", int64(7)}}},
				{query: "INSERT INTO dst(id,n) SELECT id::bigint,nextval('s') FROM src WHERE id=99"},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(5)}}}),
				{query: "CREATE TABLE bad(v TEXT)"},
				{query: "INSERT INTO bad VALUES('bad')", affected: 1},
				{query: "INSERT INTO dst(id,n) SELECT nextval('s'),v::bigint FROM bad", state: "22P02", diagnostic: &bad, errorStage: "execute"},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(6)}}}),
				{query: "SELECT id FROM dst ORDER BY id", selectRows: true, columns: []string{"id"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {int64(2)}}},
				{query: "SELECT num_nulls(nextval('s'))", selectRows: true, columns: []string{"num_nulls"}, types: []string{"INT4"}, rows: [][]any{{int64(0)}}},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(7)}}}),
				{query: "SELECT 0 IS DISTINCT FROM nextval('s') FROM src WHERE id=1", selectRows: true, columns: []string{"?column?"}, types: []string{"BOOL"}, rows: [][]any{{true}}},
				sequenceInt64Step(referenceStep{query: "SELECT currval('s')", selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(8)}}}),
			}
			for _, step := range steps {
				if len(step.types) == 5 && backend.pg {
					step.rows = [][]any{{int64(1), int64(4), time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), "\x01", nil}, {int64(2), int64(5), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), "\x02", int64(7)}}
				}
				if step.query == "INSERT INTO dst SELECT id::bigint,nextval('s'),day,payload,nullable FROM src ORDER BY id" {
					stmt, err := c.PrepareContext(p.ctx, step.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(3)}})))
					step.prepared = false
					referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: step.query}, backend.recorder, step)
				} else {
					referenceRunStep(t, p.ctx, c, backend.recorder, step)
				}
			}
		})
	}
}
