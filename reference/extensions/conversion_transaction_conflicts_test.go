package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConversionTransactionConflicts(t *testing.T) {
	for _, kind := range []string{"same_name"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
					peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE conflict_unrelated(v integer)", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
					defer holder.ExecContext(p.Ctx, "ROLLBACK")
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("SAVEPOINT claim", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(`CREATE CONVERSION conflict_one FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`, 0))
					q := `CREATE CONVERSION conflict_one FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT waiting", 0))
					fixture.DefinitionActiveCancel(t, p, b, c, holder, q, "transactionid", "ShareLock", 0, 0, "pg_conversion_name_nsp_index", func(ctx context.Context) {
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Exec("INSERT INTO conflict_unrelated VALUES(42)", 1))
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='conflict_one'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					run(fixture.Exec("SET LOCAL lock_timeout='100ms'", 0))
					_, err := c.ExecContext(p.Ctx, q)
					fixture.DefinitionIndexWaitDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "pg_conversion_name_nsp_index", b.PG)
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK TO claim", 0))
					// The canceled request is removed, the holder released, and the same
					// physical owner can now publish without stealing another identity.
					run(fixture.Exec(q, 0))
					run(fixture.Exec("RELEASE waiting", 0))
					run(fixture.Exec("COMMIT", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT v FROM conflict_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					if kind == "same_name" {
						run(fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='conflict_one'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				})
			}
		})
	}
	for _, kind := range []string{"same_name_commit", "rename_vs_drop"} {
		t.Run(kind, func(t *testing.T) {
			conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
				create := fixture.ConversionCreate("queued_conv", false)
				var namespace, oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
					t.Fatal(err)
				}
				if kind == "rename_vs_drop" {
					run(c, fixture.Exec(create, 0))
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_conversion WHERE conname='queued_conv'").Scan(&oid); err != nil {
						t.Fatal(err)
					}
				}
				run(holder, fixture.Exec("BEGIN", 0))
				q, holderSQL, lockKind, mode, class, object := create, create, "transactionid", "ShareLock", int64(0), int64(0)
				if kind == "rename_vs_drop" {
					q, holderSQL, lockKind, mode, class, object = "DROP CONVERSION queued_conv", "ALTER CONVERSION queued_conv RENAME TO queued_renamed", "object", "AccessExclusiveLock", 2607, oid
				}
				run(holder, fixture.Exec(holderSQL, 0))
				run(c, fixture.Exec("BEGIN", 0))
				join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, c, holder, q, lockKind, mode, class, object)
				run(holder, fixture.Exec("COMMIT", 0))
				outcome := join()
				want := conversionDiagnostic("42704", `conversion "queued_conv" does not exist`)
				if kind == "same_name_commit" {
					want = conversionDiagnostic("23505", `duplicate key value violates unique constraint "pg_conversion_name_nsp_index"`)
					want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_conversion", "pg_conversion_name_nsp_index"
					want.Detail = fmt.Sprintf("Key (conname, connamespace)=(queued_conv, %d) already exists.", namespace)
				}
				fixture.AssertDiagnostic(t, outcome.Err, &want)
				if outcome.Result != nil {
					t.Fatal("failed queued DDL returned result")
				}
				run(c, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
				run(c, fixture.Exec("ROLLBACK", 0))
				name := "queued_conv"
				if kind == "rename_vs_drop" {
					name = "queued_renamed"
				}
				run(c, conversionCount(name, 1))
				if oid != 0 {
					run(c, fixture.Select("SELECT oid::bigint FROM pg_conversion WHERE conname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}, name))
				}
				run(c, fixture.Exec("DROP CONVERSION "+name, 0))
				run(c, fixture.Exec(create, 0))
				run(c, fixture.Exec("DROP CONVERSION queued_conv", 0))
			})
		})
	}
}
