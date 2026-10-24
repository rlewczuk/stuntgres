package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationTransactionConflicts(t *testing.T) {
	for _, kind := range []string{"same_name", "different_name"} {
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
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(`CREATE COLLATION conflict_one FROM pg_catalog."C"`, 0))
					q := `CREATE COLLATION conflict_one FROM pg_catalog."C"`
					if kind == "different_name" {
						q = `CREATE COLLATION conflict_two FROM pg_catalog."C"`
					}
					waiterPID, holderPID := 0, 0
					if b.PG {
						waiterPID, holderPID = fixture.RowPIDs(t, p.Ctx, c, holder)
					}
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT waiting", 0))
					fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.RelationLockPending(t, p, b, waiterPID, holderPID, 3456, "ShareRowExclusiveLock", make(chan struct{}))
						}
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Exec("INSERT INTO conflict_unrelated VALUES(42)", 1))
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_collation WHERE collname='conflict_one'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					run(fixture.Exec("SET LOCAL lock_timeout='100ms'", 0))
					run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
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
						run(fixture.Select(`SELECT count(*) AS n FROM pg_collation WHERE collname='conflict_one'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				})
			}
		})
	}
	for _, kind := range []string{"same_name_commit", "rename_vs_drop", "new_column_vs_drop"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
					create := `CREATE COLLATION queued_coll FROM pg_catalog."C"`
					var namespace, oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
						t.Fatal(err)
					}
					if kind != "same_name_commit" {
						run(c, fixture.Exec(create, 0))
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_collation WHERE collname='queued_coll'").Scan(&oid); err != nil {
							t.Fatal(err)
						}
					}
					run(holder, fixture.Exec("BEGIN", 0))
					q, holderSQL, lockKind, mode, class, object := create, create, "relation", "ShareRowExclusiveLock", int64(0), int64(3456)
					if kind == "rename_vs_drop" {
						q, holderSQL, lockKind, mode, class, object = "DROP COLLATION queued_coll", "ALTER COLLATION queued_coll RENAME TO queued_renamed", "object", "AccessExclusiveLock", 3456, oid
					}
					if kind == "new_column_vs_drop" {
						q, holderSQL, lockKind, mode, class, object = "DROP COLLATION queued_coll RESTRICT", "CREATE TABLE queued_data(v text COLLATE queued_coll)", "object", "AccessExclusiveLock", 3456, oid
					}
					run(holder, fixture.Exec(holderSQL, 0))
					run(c, fixture.Exec("BEGIN", 0))
					join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, c, holder, q, lockKind, mode, class, object)
					run(holder, fixture.Exec("COMMIT", 0))
					outcome := join()
					var want fixture.Diagnostic
					switch kind {
					case "same_name_commit":
						want = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_collation_name_enc_nsp_index"`)
						want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_collation", "pg_collation_name_enc_nsp_index"
						want.Detail = fmt.Sprintf("Key (collname, collencoding, collnamespace)=(queued_coll, -1, %d) already exists.", namespace)
					case "rename_vs_drop":
						want = fixture.ControlsReferenceDiagnostic("42704", `collation "queued_coll" for encoding "UTF8" does not exist`)
					case "new_column_vs_drop":
						want = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop collation queued_coll because other objects depend on it")
						want.Detail, want.Hint = "column v of table queued_data depends on collation queued_coll", "Use DROP ... CASCADE to drop the dependent objects too."
					}
					fixture.AssertDiagnostic(t, outcome.Err, &want)
					if outcome.Result != nil {
						t.Fatal("failed queued DDL returned result")
					}
					run(c, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
					run(c, fixture.Exec("ROLLBACK", 0))
					name := "queued_coll"
					if kind == "rename_vs_drop" {
						name = "queued_renamed"
					}
					run(c, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, name))
					if oid != 0 {
						run(c, fixture.Select("SELECT oid::bigint FROM pg_collation WHERE collname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}, name))
					}
					if kind == "new_column_vs_drop" {
						run(c, fixture.Exec("INSERT INTO queued_data VALUES('é')", 1))
						run(c, fixture.Select("SELECT upper(v) AS v FROM queued_data", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
					}
					run(c, fixture.Exec("DROP COLLATION "+name+" CASCADE", 0))
					run(c, fixture.Exec(create, 0))
					run(c, fixture.Exec("DROP COLLATION queued_coll", 0))
				})
			}
		})
	}
}
