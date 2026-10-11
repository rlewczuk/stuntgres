package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerialRollbackAndRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRow := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/queryrow=%t", prepared, queryRow), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
						scalar := func(q, label, typ string, want int64) {
							if !queryRow {
								run(fixture.Select(q, []string{label}, []string{typ}, [][]any{{want}}))
								return
							}
							var got int64
							if err := conn.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != want {
								t.Fatal(q, got, want, err)
							}
						}
						run(fixture.Exec(`CREATE TABLE t(id SERIAL)`, 0))
						const query = `INSERT INTO t DEFAULT VALUES`
						held, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						insert := func() {
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, fixture.Exec(query, 1))
						}
						run(fixture.Exec(`BEGIN`, 0))
						insert()
						run(fixture.Exec(`ROLLBACK`, 0))
						run(fixture.Select(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{}))
						scalar(`SELECT currval('t_id_seq')`, "currval", "INT8", 1)
						run(fixture.Exec(`ALTER SEQUENCE t_id_seq RENAME TO renamed`, 0))
						run(fixture.Exec(`CREATE SEQUENCE t_id_seq START 100`, 0))
						insert()
						run(fixture.Select(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
						if queryRow {
							scalar(`SELECT id FROM t`, "id", "INT4", 2)
						}
						scalar(`SELECT nextval('t_id_seq')`, "nextval", "INT8", 100)
						run(fixture.Exec(`ALTER TABLE t RENAME COLUMN id TO renamed_id`, 0))
						run(fixture.Exec(`ALTER TABLE t RENAME TO renamed_table`, 0))
						run(fixture.Exec(`INSERT INTO renamed_table DEFAULT VALUES`, 1))
						run(fixture.Select(`SELECT * FROM renamed_table ORDER BY renamed_id`, []string{"renamed_id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
						if err := held.Close(); err != nil {
							t.Fatal(err)
						}
						if err := conn.Close(); err != nil {
							t.Fatal(err)
						}
						rows, err := backend.DB.Query(`SELECT renamed_id FROM renamed_table ORDER BY renamed_id`)
						fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select(`SELECT renamed_id FROM renamed_table ORDER BY renamed_id`, []string{"renamed_id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
					})
				}
			})
		}
	}
}

func TestReferenceSerialCandidateCheck(t *testing.T) {
	serialReferenceStreams(t, false, func(bool) []fixture.Step {
		return []fixture.Step{
			fixture.Exec(`CREATE TABLE t(id SERIAL,CHECK(nextval('t_id_seq')>0))`, 0),
			fixture.Select(`SELECT last_value,is_called FROM t_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			fixture.Exec(`INSERT INTO t DEFAULT VALUES`, 1),
			fixture.Select(`SELECT * FROM t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT currval('t_id_seq')`, []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Exec(`DROP TABLE t RESTRICT`, 0),
			fixture.Exec(`CREATE SEQUENCE t_id_seq`, 0),
		}
	})
}
