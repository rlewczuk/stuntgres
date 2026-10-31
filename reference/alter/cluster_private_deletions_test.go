package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func clusterDeletionRoutineIndexes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE FUNCTION public.cluster_delete_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+10$$",
		"CREATE INDEX delete_btree ON t(public.cluster_delete_key(k))",
		"CREATE INDEX delete_hash ON t USING hash(public.cluster_delete_key(k))",
		"CREATE INDEX delete_brin ON t USING brin(public.cluster_delete_key(k)) WITH(pages_per_range=1)",
		"CREATE INDEX delete_partial ON t(public.cluster_delete_key(k)) WHERE public.cluster_delete_key(k)>11",
	} {
		persistenceExec(t, p, b, c, q)
	}
}

func clusterRemedyNativeRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, rows [][]any, flags fixture.Step) {
	t.Helper()
	old, err := c.QueryContext(p.Ctx, "SELECT id,k,payload FROM public.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	persistenceRun(t, p, b, c, fixture.Select("SELECT id,k,payload FROM public.t ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, rows))
	persistenceRun(t, p, b, c, flags)
	fixture.StringCoreRetainedRows(t, old, nil, []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, rows, false)
}

func TestReferenceClusterPrivateDeletions(t *testing.T) {
	for _, scenario := range []string{"insert_delete_reinsert", "update_delete", "captured_own_delete_reinsert"} {
		for _, driver := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					name := fmt.Sprintf("%s/driver=%t/held=%t/commit=%t", scenario, driver, held, commit)
					t.Run(name, func(t *testing.T) {
						persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							clusterCommandSeed(t, p, b, c)
							clusterDeletionRoutineIndexes(t, p, b, c)
							var executor fixture.Executor = c
							var tx *sql.Tx
							if driver {
								var err error
								tx, err = c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								executor = tx
							} else {
								persistenceExec(t, p, b, c, "BEGIN")
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							if scenario == "captured_own_delete_reinsert" {
								persistenceRun(t, p, b, executor, fixture.Exec("INSERT INTO t VALUES(6,0,'captured')", 1))
							}
							const clusterSQL = "CLUSTER t USING a"
							if held {
								statement, err := executor.PrepareContext(p.Ctx, clusterSQL)
								if err != nil {
									t.Fatal(err)
								}
								defer statement.Close()
								persistenceRun(t, p, b, fixture.HeldStatement{Executor: executor, Statement: statement, SQL: clusterSQL}, fixture.Exec(clusterSQL, 0))
							} else {
								persistenceExec(t, p, b, executor, clusterSQL)
							}
							if scenario == "update_delete" {
								persistenceRun(t, p, b, executor, fixture.Exec("UPDATE t SET k=9,payload='deleted' WHERE id=2", 1))
								persistenceRun(t, p, b, executor, fixture.Exec("DELETE FROM t WHERE id=2", 1))
							} else {
								if scenario == "insert_delete_reinsert" {
									persistenceRun(t, p, b, executor, fixture.Exec("INSERT INTO t VALUES(6,4,'first')", 1))
								}
								persistenceRun(t, p, b, executor, fixture.Exec("DELETE FROM t WHERE id=6", 1))
								persistenceRun(t, p, b, executor, fixture.Exec("INSERT INTO t VALUES(6,5,'replacement')", 1))
							}
							want := clusterCommandRows().Rows
							if commit {
								if scenario == "update_delete" {
									want = [][]any{{int64(1), int64(2), "b"}, {int64(3), int64(2), "b"}, {int64(4), nil, nil}, {int64(5), int64(3), "c"}}
								} else {
									want = append(append([][]any(nil), want...), []any{int64(6), int64(5), "replacement"})
								}
							}
							if driver {
								var err error
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							} else {
								end := "ROLLBACK"
								if commit {
									end = "COMMIT"
								}
								persistenceExec(t, p, b, c, end)
							}
							rows := fixture.Select("SELECT id,k,payload FROM t ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, want)
							persistenceRun(t, p, b, c, rows)
							persistenceRun(t, p, b, c, fixture.Prepared(rows))
							persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(want))}}))
							keyRows := [][]any{}
							if commit && scenario != "update_delete" {
								keyRows = [][]any{{int64(6)}}
							}
							persistenceRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE public.cluster_delete_key(k)=15 ORDER BY id", []string{"id"}, []string{"INT4"}, keyRows)))
							flags := clusterState("t", [][]any{{"a", commit}, {"b", false}, {"delete_brin", false}, {"delete_btree", false}, {"delete_hash", false}, {"delete_partial", false}, {"u", false}})
							persistenceRun(t, p, b, c, flags)
							if !b.PG && driver && held && commit {
								clusterRemedyNativeRoundtrip(t, p, b, c, want, flags)
								persistenceRun(t, p, b, c, rows)
							}
						})
					})
				}
			}
		}
	}
}
