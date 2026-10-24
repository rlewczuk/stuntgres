package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaQueryCreate(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprint(held, goTx, commit), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							fixture.LockExec(t, p.Ctx, c, "CREATE SEQUENCE tx_query_counter", 0)
							var e fixture.Executor = c
							finish := func() {}
							if goTx {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								}
							} else {
								fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
								defer c.ExecContext(p.Ctx, "ROLLBACK")
								finish = func() {
									q := "ROLLBACK"
									if commit {
										q = "COMMIT"
									}
									fixture.LockExec(t, p.Ctx, c, q, 0)
								}
							}
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							run(fixture.Exec("CREATE TABLE tx_query_source(v INT)", 0))
							run(fixture.Exec("INSERT INTO tx_query_source VALUES(3),(7)", 2))
							for _, tc := range []struct {
								q, name string
								count   int64
								rows    [][]any
							}{
								{"CREATE TABLE tx_query_data AS SELECT v FROM tx_query_source WITH DATA", "tx_query_data", 2, [][]any{{int64(3)}, {int64(7)}}},
								{"CREATE TABLE tx_query_empty AS SELECT v FROM tx_query_source WITH NO DATA", "tx_query_empty", 0, nil},
								{"SELECT v INTO tx_query_into FROM tx_query_source", "tx_query_into", 2, [][]any{{int64(3)}, {int64(7)}}},
							} {
								if held {
									stmt, err := e.PrepareContext(p.Ctx, tc.q)
									if err != nil {
										t.Fatal(err)
									}
									fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: tc.q}, b.Recorder, fixture.Exec(tc.q, tc.count))
									stmt.Close()
								} else {
									run(fixture.Exec(tc.q, tc.count))
								}
								run(fixture.Select("SELECT v FROM "+tc.name+" ORDER BY v", []string{"v"}, []string{"INT4"}, tc.rows))
								run(fixture.Select("SELECT attname,atttypmod,attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypmod", "attnotnull"}, []string{"NAME", "INT4", "BOOL"}, [][]any{{"v", int64(-1), false}}, tc.name))
							}
							run(fixture.Exec("CREATE TABLE tx_query_no_eval AS SELECT 1/0 AS v FROM tx_query_source WITH NO DATA", 0))
							run(fixture.Select("SELECT v FROM tx_query_no_eval", []string{"v"}, []string{"INT4"}, nil))
							run(fixture.Exec("CREATE TABLE tx_query_once AS SELECT nextval('tx_query_counter') AS n FROM tx_query_source", 2))
							run(fixture.Select("SELECT n FROM tx_query_once ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
							run(fixture.Exec("CREATE TABLE tx_query_no_next AS SELECT nextval('tx_query_counter') AS n FROM tx_query_source WITH NO DATA", 0))
							run(fixture.Select("SELECT currval('tx_query_counter')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
							run(fixture.Exec("INSERT INTO tx_query_source VALUES(11)", 1))
							run(fixture.Select("SELECT v FROM tx_query_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(7)}}))
							absence := fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('tx_query_source','tx_query_data','tx_query_empty','tx_query_into','tx_query_no_eval')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, absence)
							finish()
							if commit {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM tx_query_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(7)}}))
							} else {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, absence)
							}
						})
					}
				})
			}
		}
	}
}
