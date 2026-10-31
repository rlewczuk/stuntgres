package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func identNativePool(t *testing.T, d *stuntgres.Driver, dsn string) (*sql.DB, fixture.Backend) {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() { raw.Close() })
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	return raw, fixture.Backend{Name: "memory", DB: recorded, Recorder: recorder}
}

func TestReferencePGIdentFileMappingsStartupSQL(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, nil, prepared)
			}
			files.Write(t, p.Ctx, b, "", "attached s p\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identValid(1, files.Path(b, ""), 1, "attached", "s", "p")}, false)
			if b.PG {
				return
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SHOW ident_file", []string{"ident_file"}, []string{"TEXT"}, [][]any{{"/stuntgres/simulated/pg_ident.conf"}}))
			for _, d := range []*stuntgres.Driver{stuntgres.NewDriver(), new(stuntgres.Driver)} {
				_, unattached := identNativePool(t, d, "unattached")
				for _, prepared := range []bool{false, true} {
					identRead(t, p, unattached, unattached.DB, identSelect, nil, prepared)
					s := fixture.Exec(identSelect, 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, unattached.DB, unattached.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, unattached.DB, unattached.Recorder, fixture.Exec("CREATE ROLE ident_unattached_actor", 0))
				actor, err := unattached.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, actor, unattached.Recorder, fixture.Exec("SET ROLE ident_unattached_actor", 0))
				identDenied(t, p, unattached, actor, identSelect, false)
				actor.Close()
			}
			d := p.NativeConnector.Driver().(*stuntgres.Driver)
			_, sameDriver := identNativePool(t, d, p.NativeDSN+" other opaque key")
			identRead(t, p, sameDriver, sameDriver.DB, identSelect, [][]any{identValid(1, files.Path(b, ""), 1, "attached", "s", "p")}, true)
			otherPath := filepath.Join(t.TempDir(), "other.conf")
			if err := os.WriteFile(otherPath, []byte("isolated other db\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			otherDriver := stuntgres.NewDriver()
			if err := otherDriver.ConfigureIdentFile(otherPath); err != nil {
				t.Fatal(err)
			}
			_, other := identNativePool(t, otherDriver, p.NativeDSN)
			identRead(t, p, other, other.DB, identSelect, [][]any{identValid(1, otherPath, 1, "isolated", "other", "db")}, false)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(ctx, identSelect)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestReferencePGIdentFileMappingsLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ident_lifecycle_actor"})
	p, files := fixture.NewIdentFileScenario(t, "lifecycle s p\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			run(admin, "CREATE ROLE ident_lifecycle_actor")
			actorReturned := false
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if !actorReturned {
					if _, err := actor.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
					}
				}
				for _, q := range []string{"REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM ident_lifecycle_actor", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM ident_lifecycle_actor"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			want := [][]any{identValid(1, files.Path(b, ""), 1, "lifecycle", "s", "p")}
			stmt, err := actor.PrepareContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: identSelect}
			run(actor, "BEGIN")
			run(actor, "SET ROLE ident_lifecycle_actor")
			run(actor, "ROLLBACK")
			run(actor, "SET ROLE ident_lifecycle_actor")
			identDenied(t, p, b, held, identSelect, false)
			run(actor, "RESET ROLE")
			identRead(t, p, b, held, identSelect, want, true)
			run(admin, "GRANT SELECT ON pg_catalog.pg_ident_file_mappings TO ident_lifecycle_actor")
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO ident_lifecycle_actor")
			run(actor, "SET SESSION AUTHORIZATION ident_lifecycle_actor")
			identRead(t, p, b, held, identSelect, want, true)
			run(actor, "RESET SESSION AUTHORIZATION")
			// Returned physical sessions and newly borrowed sessions retain startup authority.
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := actor.Close(); err != nil {
				t.Fatal(err)
			}
			actorReturned = true
			identRead(t, p, b, b.DB, identSelect, want, true)
			if b.PG {
				return
			}
			// Both the private template seed and new logical runtime inherit the capability.
			run(admin, "CREATE DATABASE ident_lifecycle_database TEMPLATE template1")
			connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenDatabaseConnector(p.NativeDSN, "ident_lifecycle_database")
			if err != nil {
				t.Fatal(err)
			}
			db, recorder := fixture.RecordedNativePool(t, connector)
			databaseBackend := fixture.Backend{Name: "memory", DB: db, Recorder: recorder}
			identRead(t, p, databaseBackend, db, identSelect, want, false)
			// Closing all pools made from a fresh exact DSN does not clear Driver startup state.
			d := p.NativeConnector.Driver().(*stuntgres.Driver)
			raw, reconnect := identNativePool(t, d, "ident-all-pools-close")
			identRead(t, p, reconnect, reconnect.DB, identSelect, want, false)
			reconnect.DB.Close()
			raw.Close()
			_, reopened := identNativePool(t, d, "ident-all-pools-close")
			identRead(t, p, reopened, reopened.DB, identSelect, want, true)
		})
	}
}
