package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceSerialRollbackAndRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRow := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/queryrow=%t", prepared, queryRow), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						conn, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
						scalar := func(q, label, typ string, want int64) {
							if !queryRow {
								run(referenceSelect(q, []string{label}, []string{typ}, [][]any{{want}}))
								return
							}
							var got int64
							if err := conn.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != want {
								t.Fatal(q, got, want, err)
							}
						}
						run(referenceExec(`CREATE TABLE t(id SERIAL)`, 0))
						const query = `INSERT INTO t DEFAULT VALUES`
						held, err := conn.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						insert := func() {
							referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}, backend.recorder, referenceExec(query, 1))
						}
						run(referenceExec(`BEGIN`, 0))
						insert()
						run(referenceExec(`ROLLBACK`, 0))
						run(referenceSelect(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{}))
						scalar(`SELECT currval('t_id_seq')`, "currval", "INT8", 1)
						run(referenceExec(`ALTER SEQUENCE t_id_seq RENAME TO renamed`, 0))
						run(referenceExec(`CREATE SEQUENCE t_id_seq START 100`, 0))
						insert()
						run(referenceSelect(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
						if queryRow {
							scalar(`SELECT id FROM t`, "id", "INT4", 2)
						}
						scalar(`SELECT nextval('t_id_seq')`, "nextval", "INT8", 100)
						run(referenceExec(`ALTER TABLE t RENAME COLUMN id TO renamed_id`, 0))
						run(referenceExec(`ALTER TABLE t RENAME TO renamed_table`, 0))
						run(referenceExec(`INSERT INTO renamed_table DEFAULT VALUES`, 1))
						run(referenceSelect(`SELECT * FROM renamed_table ORDER BY renamed_id`, []string{"renamed_id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
						if err := held.Close(); err != nil {
							t.Fatal(err)
						}
						if err := conn.Close(); err != nil {
							t.Fatal(err)
						}
						rows, err := backend.db.Query(`SELECT renamed_id FROM renamed_table ORDER BY renamed_id`)
						deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect(`SELECT renamed_id FROM renamed_table ORDER BY renamed_id`, []string{"renamed_id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
					})
				}
			})
		}
	}
}

func TestReferenceSerialCandidateCheck(t *testing.T) {
	serialReferenceStreams(t, false, func(bool) []referenceStep {
		return []referenceStep{
			referenceExec(`CREATE TABLE t(id SERIAL,CHECK(nextval('t_id_seq')>0))`, 0),
			referenceSelect(`SELECT last_value,is_called FROM t_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			referenceExec(`INSERT INTO t DEFAULT VALUES`, 1),
			referenceSelect(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect(`SELECT currval('t_id_seq')`, []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}),
			referenceExec(`DROP TABLE t RESTRICT`, 0),
			referenceExec(`CREATE SEQUENCE t_id_seq`, 0),
		}
	})
}
