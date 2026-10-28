package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"reflect"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterStorageAuthorizationAndCancellation(t *testing.T) {
	t.Run("authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"storage_owner", "storage_member", "storage_reader", "storage_child_owner"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE storage_owner", "CREATE ROLE storage_member", "CREATE ROLE storage_reader", "CREATE ROLE storage_child_owner", "GRANT storage_owner TO storage_member", "GRANT USAGE ON SCHEMA public TO storage_owner,storage_member,storage_reader,storage_child_owner", "CREATE TABLE storage_case(i integer,t text,n numeric)", "ALTER TABLE storage_case OWNER TO storage_owner", "GRANT ALL ON storage_case TO storage_reader"} { run(fixture.Exec(q, 0)) }
			for _, action := range []string{"STORAGE EXTERNAL", "COMPRESSION pglz"} {
				run(fixture.Exec("SET ROLE storage_reader", 0))
				for _, prepared := range []bool{false, true} {
					s := storageError("ALTER TABLE storage_case ALTER t SET "+action, "42501", "must be owner of table storage_case")
					s.Prepared = prepared
					if prepared { s.ErrorStage = "execute" }
					run(s)
				}
				run(fixture.Exec("RESET ROLE", 0)); run(storageInitial())
			}
			for _, role := range []string{"storage_owner", "storage_member"} {
				run(fixture.Exec("SET ROLE "+role, 0))
				run(fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0)))
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE DEFAULT, ALTER t SET COMPRESSION default", 0))
			run(fixture.Select("SELECT tableowner,has_table_privilege('storage_reader','storage_case','SELECT') AS allowed FROM pg_tables WHERE schemaname='public' AND tablename='storage_case'", []string{"tableowner", "allowed"}, []string{"NAME", "BOOL"}, [][]any{{"storage_owner", true}}))
			for _, action := range []string{"STORAGE EXTERNAL", "COMPRESSION pglz"} {
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(storageError("ALTER TABLE storage_case ALTER t SET "+action, "25006", "cannot execute ALTER TABLE in a read-only transaction"))
				run(fixture.Exec("ROLLBACK", 0)); run(storageInitial())
			}
			for _, q := range []string{"CREATE TABLE storage_child() INHERITS(storage_case)", "CREATE INDEX storage_child_idx ON storage_child(t)", "ALTER TABLE storage_child OWNER TO storage_child_owner", "SET ROLE storage_owner"} { run(fixture.Exec(q, 0)) }
			run(fixture.Prepared(storageError("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL", "42501", "must be owner of table storage_child")))
			run(fixture.Exec("RESET ROLE", 0)); run(storageInitial())
			run(storageMetadata("storage_child", []any{"i", "p", ""}, []any{"t", "x", ""}, []any{"n", "m", ""}))
			run(storageMetadata("storage_child_idx", []any{"t", "x", ""}))
		})
	})
	for _, tc := range []struct{ target, action, storage string }{
		{"storage_case", "STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", "e"},
		{"storage_child", "STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", "e"},
		{"storage_case", "COMPRESSION pglz", "x"},
	} {
		target := tc.target
		for _, cancelWorker := range []bool{false, true} {
			t.Run(fmt.Sprintf("lock/%s/%s/cancel=%v", target, tc.action, cancelWorker), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE storage_case(i integer,t text,n numeric)", "INSERT INTO storage_case VALUES(7,'payload',12)", "CREATE TABLE storage_child() INHERITS(storage_case)", "CREATE INDEX storage_idx ON storage_case(t)", "CREATE INDEX storage_child_idx ON storage_child(t)"} {
						n := int64(0); if q == "INSERT INTO storage_case VALUES(7,'payload',12)" { n = 1 }; run(fixture.Exec(q, n))
					}
					query := "ALTER TABLE storage_case ALTER t SET "+tc.action
					held := addConstraintHeld(t, p, b, c, query)
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG { waiterPID = fixture.LockPID(t, p.Ctx, c); holderPID = fixture.LockPID(t, p.Ctx, holder); oid = fixture.LockOID(t, p.Ctx, target) }
					tx, err := holder.BeginTx(p.Ctx, nil); if err != nil { t.Fatal(err) }; defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE ONLY "+target+" IN SHARE MODE", 0))
					ctx, cancel := context.WithCancel(p.Ctx); defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						result, err := held.ExecContext(ctx, query); close(finished)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
					unchanged := func() {
						for _, relation := range []string{"storage_case", "storage_child"} { fixture.RunStep(t, p.Ctx, observer, b.Recorder, storageMetadata(relation, []any{"i", "p", ""}, []any{"t", "x", ""}, []any{"n", "m", ""})) }
						for _, relation := range []string{"storage_idx", "storage_child_idx"} { fixture.RunStep(t, p.Ctx, observer, b.Recorder, storageMetadata(relation, []any{"t", "x", ""})) }
					}
					unchanged()
					if cancelWorker {
						if b.PG {
							var ok bool; if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&ok); err != nil || !ok { t.Fatal(ok, err) }
						} else { cancel() }
					} else if err := tx.Commit(); err != nil { t.Fatal(err) }
					out := await()
					if cancelWorker {
						if out.Result != nil { t.Fatal("canceled ALTER returned a result") }
						if b.PG { d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request"); fixture.AssertDiagnostic(t, out.Err, &d) } else if !errors.Is(out.Err, context.Canceled) { t.Fatal(out.Err) }
						unchanged()
						if err := tx.Commit(); err != nil { t.Fatal(err) }
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
					} else {
						if out.Err != nil || out.Result == nil { t.Fatal(out.Result, out.Err) }
						if n, err := out.Result.RowsAffected(); err != nil || n != 0 { t.Fatal(n, err) }
					}
					run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", tc.storage, "p"}, []any{"n", "m", ""}))
					run(storageMetadata("storage_child", []any{"i", "p", ""}, []any{"t", tc.storage, ""}, []any{"n", "m", ""}))
					run(storageMetadata("storage_idx", []any{"t", tc.storage, "p"})); run(storageMetadata("storage_child_idx", []any{"t", tc.storage, ""})); run(storageRows())
				})
			})
		}
	}
}

func TestReferenceAlterStoragePreparedInvalidation(t *testing.T) {
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE storage_case(i integer NOT NULL,t text,n numeric)", 0))
		run(fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
		initial := storageInitial()
		held := addConstraintHeld(t, p, b, c, initial.Query)
		data := addConstraintHeld(t, p, b, c, "SELECT i,t,n FROM storage_case ORDER BY i")
		check := func(storage, compression string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", storage, compression}, []any{"n", "m", ""}))
			s := fixture.Select(data.SQL, []string{"i", "t", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(7), "payload", "12"}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				ct, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
				fixture.TableDDLColumnTypes(t, b.PG, ct, []string{"i", "t", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, []bool{false, true, true})
			}
			fixture.RunStep(t, p.Ctx, data, b.Recorder, s)
		}
		check("x", "")
		var old *sql.Rows
		if !b.PG {
			var err error
			old, err = p.Native.QueryContext(p.Ctx, initial.Query)
			if err != nil { t.Fatal(err) }
			defer old.Close()
		}
		run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0)); check("e", "p")
		if old != nil {
			columns, err := old.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"attname", "attstorage", "attcompression"}) { t.Fatal(columns, err) }
			types, err := old.ColumnTypes()
			if err != nil { t.Fatal(err) }
			for i, want := range []string{"NAME", "TEXT", "TEXT"} {
				if types[i].DatabaseTypeName() != want { t.Fatal("retained catalog type", types[i].DatabaseTypeName(), want) }
			}
			for _, want := range [][]any{{"i", "p", ""}, {"t", "x", ""}, {"n", "m", ""}} {
				if !old.Next() { t.Fatal("lost prechange catalog row", old.Err()) }
				var name, storage, compression string
				if err := old.Scan(&name, &storage, &compression); err != nil { t.Fatal(err) }
				if !reflect.DeepEqual([]any{name, storage, compression}, want) { t.Fatal("prechange snapshot mutated", name, storage, compression) }
			}
			if old.Next() || old.Err() != nil { t.Fatal("retained catalog termination", old.Err()) }
			if err := old.Close(); err != nil { t.Fatal(err) }
		}
		run(fixture.Prepared(storageError("ALTER TABLE storage_case ALTER t SET STORAGE MAIN, ALTER t SET COMPRESSION bogus", "22023", `invalid compression method "bogus"`))); check("e", "p")
		run(fixture.Exec("BEGIN", 0)); run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE MAIN", 0)); run(fixture.Exec("ROLLBACK", 0)); check("e", "p")
		run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE DEFAULT, ALTER t SET COMPRESSION default", 0)); check("x", "")
	})
}
