package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionTransactions(t *testing.T) {
	for _, route := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", route, held), func(t *testing.T) {
				conversionScenario(t, []string{"conv_tx_hidden", "conv_tx_dest"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conversionExec(t, p, b, "CREATE SCHEMA conv_tx_hidden", "CREATE SCHEMA conv_tx_dest")
					conversionHideSeed(t, p, b, "conv_tx_hidden")
					publicOID := conversionNamespaceOID(t, p, b, "public")
					destOID := conversionNamespaceOID(t, p, b, "conv_tx_dest")
					c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					for _, x := range []*sql.Conn{c, peer} {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec("SET search_path=public", 0))
					}
					queries := []string{fixture.ConversionCreate("conv_tx", true), "ALTER CONVERSION conv_tx RENAME TO conv_tx_renamed", "ALTER CONVERSION conv_tx_renamed SET SCHEMA conv_tx_dest", "DROP CONVERSION conv_tx_dest.conv_tx_renamed"}
					statements := map[string]*sql.Stmt{}
					if held {
						for _, q := range append(append([]string{}, queries...), conversionValue().Query) {
							s, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal("quiet Prepare", q, err)
							}
							defer s.Close()
							statements[q] = s
						}
					}
					missing := func(x fixture.Executor) {
						s := fixture.ErrorDiagnostic(conversionValue().Query, conversionMissingDefault())
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					}
					missing(peer)
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
						run(conversionValue())
						var oid int64
						rows, err := x.QueryContext(p.Ctx, "SELECT oid::bigint FROM pg_conversion WHERE conname='conv_tx'")
						if err != nil {
							t.Fatal(err)
						}
						if !rows.Next() {
							t.Fatal("private conversion missing", rows.Err())
						}
						if err = rows.Scan(&oid); err != nil || oid <= 0 {
							t.Fatal(oid, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("identity multiplicity", rows.Err())
						}
						rows.Close()
						// REGPROC/OID adapter normalization is independently covered by
						// conversionMetadata; these exact logical fields work on Tx too.
						metadata := func(name string, namespace int64) fixture.Step {
							return fixture.Select("SELECT oid::bigint,conname,connamespace::bigint,conforencoding,contoencoding,conproc::text,condefault FROM pg_conversion WHERE oid=$1", []string{"oid", "conname", "connamespace", "conforencoding", "contoencoding", "conproc", "condefault"}, []string{"INT8", "NAME", "INT8", "INT4", "INT4", "TEXT", "BOOL"}, [][]any{{oid, name, namespace, int64(6), int64(8), "utf8_to_iso8859_1", true}}, oid)
						}
						run(metadata("conv_tx", publicOID))
						if route != "autocommit" {
							missing(peer)
							run(fixture.Exec("SAVEPOINT original", 0))
						} else {
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, conversionValue())
						}
						run(fixture.Exec(queries[1], 0))
						run(fixture.Exec(queries[2], 0))
						run(metadata("conv_tx_renamed", destOID))
						// Moving outside search_path actually removes dispatch.
						if route != "autocommit" {
							run(fixture.Exec("SAVEPOINT moved", 0))
						}
						missing(x)
						if route != "autocommit" {
							run(fixture.Exec("ROLLBACK TO moved", 0))
						}
						run(fixture.Exec(queries[3], 0))
						run(conversionCount("conv_tx_renamed", 0))
						if route != "autocommit" {
							for range 2 {
								run(fixture.Exec("ROLLBACK TO original", 0))
								run(metadata("conv_tx", publicOID))
								run(conversionValue())
							}
							run(fixture.Exec("RELEASE original", 0))
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
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, conversionValue())
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("DROP CONVERSION conv_tx", 0))
							} else {
								missing(peer)
							}
						}
					}
				})
			})
		}
	}
}

func TestReferenceConversionTransactionAuthority(t *testing.T) {
	conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		conversionExec(t, p, b, fixture.ConversionCreate("conv_tx_existing", false))
		oid := conversionOID(t, p, b, "conv_tx_existing")
		publicOID := conversionNamespaceOID(t, p, b, "public")
		for _, q := range []string{fixture.ConversionCreate("conv_tx_readonly", false), "ALTER CONVERSION conv_tx_existing RENAME TO conv_tx_renamed", "DROP CONVERSION conv_tx_existing"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, held := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
				var x fixture.Executor = c
				if held {
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				command := "CREATE"
				if q[0] == 'A' {
					command = "ALTER"
				}
				if q[0] == 'D' {
					command = "DROP"
				}
				s := fixture.ErrorDiagnostic(q, conversionDiagnostic("25006", "cannot execute "+command+" CONVERSION in a read-only transaction"))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", conversionDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
				conversionStep(t, p, b, conversionMetadata(oid, "conv_tx_existing", publicOID, false))
				conversionStep(t, p, b, conversionCount("conv_tx_readonly", 0))
				conversionStep(t, p, b, conversionCount("conv_tx_renamed", 0))
			}
		}
	})
}
