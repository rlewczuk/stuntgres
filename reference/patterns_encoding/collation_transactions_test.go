package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Each cell owns a physical session. Held utilities are prepared once before
// BEGIN and reused after rollback-to and in a later transaction.
func TestReferenceCollationTransactions(t *testing.T) {
	for _, route := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", route, held), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "coll_tx_schema")
				fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					queries := []string{`CREATE COLLATION coll_tx FROM pg_catalog."C"`, `ALTER COLLATION coll_tx RENAME TO coll_tx_renamed`, `ALTER COLLATION coll_tx_renamed SET SCHEMA coll_tx_schema`, `DROP COLLATION coll_tx_schema.coll_tx_renamed`}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA coll_tx_schema", 0))
					statements := map[string]*sql.Stmt{}
					if held {
						for _, q := range queries {
							s, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal("quiet utility Prepare", q, err)
							}
							defer s.Close()
							statements[q] = s
						}
					}
					peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					count := func(x fixture.Executor, name string, n int64) {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT count(*) FROM pg_collation WHERE collname=$1", []string{"count"}, []string{"INT8"}, [][]any{{n}}, name))
					}
					count(c, "coll_tx", 0)
					for _, commit := range []bool{false, true} {
						var x fixture.Executor = c
						var tx *sql.Tx
						if route == "go" {
							var err error
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							x = tx
						} else if route == "sql" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
						}
						run := func(s fixture.Step) {
							var executor fixture.Executor = x
							if stmt := statements[s.Query]; stmt != nil {
								if tx != nil {
									stmt = tx.StmtContext(p.Ctx, stmt)
									defer stmt.Close()
								}
								executor = fixture.HeldStatement{Executor: x, Statement: stmt, SQL: s.Query}
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						}
						run(fixture.Exec(queries[0], 0))
						var oid int64
						rows, err := x.QueryContext(p.Ctx, "SELECT oid::bigint FROM pg_collation WHERE collname='coll_tx'")
						if err != nil {
							t.Fatal(err)
						}
						if !rows.Next() {
							t.Fatal("private collation missing", rows.Err())
						}
						if err = rows.Scan(&oid); err != nil || oid <= 0 {
							t.Fatal(oid, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("private identity multiplicity", rows.Err())
						}
						rows.Close()
						use := fixture.Select(`SELECT upper('é' COLLATE coll_tx) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}})
						consumer, err := x.PrepareContext(p.Ctx, use.Query)
						if err != nil {
							t.Fatal(err)
						}
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: x, Statement: consumer, SQL: use.Query}, b.Recorder, use)
						consumer.Close()
						run(fixture.Select("SELECT collprovider,collencoding,collisdeterministic,collcollate,collctype,colllocale,collversion FROM pg_collation WHERE oid=$1", []string{"collprovider", "collencoding", "collisdeterministic", "collcollate", "collctype", "colllocale", "collversion"}, []string{"CHAR", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"c", int64(-1), true, "C", "C", nil, nil}}, oid))
						if route != "autocommit" {
							count(peer, "coll_tx", 0)
							d := fixture.ControlsReferenceDiagnostic("42704", `collation "coll_tx" for encoding "UTF8" does not exist`)
							d.Position = "12"
							s := fixture.ErrorDiagnostic(`SELECT 'a' COLLATE coll_tx`, d)
							s.SelectRows = true
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
							run(fixture.Exec("SAVEPOINT prior", 0))
						} else {
							count(peer, "coll_tx", 1)
						}
						for _, q := range queries[1:3] {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Select("SELECT oid::bigint FROM pg_collation WHERE collname='coll_tx_renamed'", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}))
						run(fixture.Select(`SELECT upper('é' COLLATE coll_tx_schema.coll_tx_renamed) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
						run(fixture.Exec(queries[3], 0))
						count(x, "coll_tx_renamed", 0)
						if route != "autocommit" {
							for range 2 {
								run(fixture.Exec("ROLLBACK TO prior", 0))
								run(use)
							}
							run(fixture.Select("SELECT oid::bigint FROM pg_collation WHERE collname='coll_tx'", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}))
							run(fixture.Exec("RELEASE prior", 0))
							if tx != nil {
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
								run(fixture.Exec(end, 0))
							}
							if commit {
								count(peer, "coll_tx", 1)
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, use)
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("DROP COLLATION coll_tx", 0))
							} else {
								count(peer, "coll_tx", 0)
							}
						}
					}
				})
			})
		}
	}
}
