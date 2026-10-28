package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestDatabaseSettingsCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "latest_row", "reauthorize", "same_name_replacement"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			if mode == "reauthorize" {
				e.Mu.Lock()
				base := e.RoleStore.Published
				d, _ := base.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
				d.Superuser = false
				root, err := base.Root.PutDescriptor(d)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				e.RoleStore.PublishLocked(base.WithRoot(root))
				e.Mu.Unlock()
			}
			holder, holderDone := enginetest.RoleOperation(t, e, "postgres")
			defer holderDone()
			tx, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			holderActive := true
			defer func() {
				if holderActive {
					_, _ = e.Rollback(holder, tx)
				}
			}()
			statement := databaseSettingsStatement(e, "waiter")
			key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
			// The alternate spelling owns the same resource and private row.
			_, err = rolecmds.ExecuteRoleSettings(e, databaseSettingsScope(holder, holder.Session.Transactions), &parser.AlterRoleSettings{All: true, Database: &statement.Database, Set: &parser.Set{Name: statement.Set.Name, Values: []string{"holder"}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			before := e.RoleStore.Published
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			waiter, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			waiterTx, err := e.Begin(waiter, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			waiterActive := true
			cleanupWaiter := func() {
				if !waiterActive {
					return
				}
				waiterActive = false
				if waiter.Context().Err() != nil {
					_, _ = e.Commit(waiter, waiterTx)
				} else {
					_, _ = e.Rollback(waiter, waiterTx)
				}
			}
			defer cleanupWaiter()
			result := make(chan error, 1)
			joined := false
			await := func() error {
				t.Helper()
				select {
				case err := <-result:
					joined = true
					return err
				case <-time.After(5 * time.Second):
					t.Fatal("database settings waiter did not terminate")
					return nil
				}
			}
			defer func() {
				if !joined {
					cancel()
					await()
				}
			}()
			go func() {
				_, err := rolecmds.ExecuteDatabaseSettings(e, databaseSettingsScope(waiter, waiter.Session.Transactions), statement, 0)
				result <- err
			}()
			enginetest.LockPending(t, e, 1)
			if mode == "cancel" {
				cancel()
				enginetest.LockAwait(t, result, context.Canceled)
				joined = true
				if e.RoleStore.Published != before || len(waiter.Notices) != 0 || before.Settings.Lookup(key) != nil {
					t.Fatal("admitted cancellation published metadata/notices")
				}
			} else {
				if mode == "reauthorize" || mode == "same_name_replacement" {
					successor := e.Roles().LookupRef("pg_read_all_data")
					e.Mu.Lock()
					next := catalog.CloneState(e.Catalog.State)
					db := *next.MustDatabase(key.Database)
					if mode == "reauthorize" {
						db.Owner = successor
						next.SetDescriptor(key.Database, &db)
					} else {
						replacement := db
						replacement.ObjectAddress.ObjectID += 100000
						db.ObjectName = "retained_old_database"
						next.SetDescriptor(key.Database, &db)
						next.SetDescriptor(replacement.Address(), &replacement)
						next.BindName(catalog.DescriptorNameKey(&replacement), replacement.Address())
					}
					e.Catalog.PublishLocked(next)
					e.Mu.Unlock()
					_, err = e.Rollback(holder, tx)
				} else {
					_, err = e.Commit(holder, tx)
				}
				holderActive = false
				if err != nil {
					t.Fatal(err)
				}
				got := await()
				if mode == "reauthorize" {
					var d *sqlstate.Error
					if !errors.As(got, &d) || d.Code != "42501" || d.Message != "must be owner of database "+statement.Database.Text || e.RoleStore.Published != before {
						t.Fatal("wait reused stale ownership", got)
					}
				} else if mode == "same_name_replacement" {
					if !errors.Is(got, ids.ErrCatalogReplaced) || e.RoleStore.Published != before {
						t.Fatal("wait redirected to same-name replacement", got)
					}
				} else {
					if got != nil {
						t.Fatal(got)
					}
					private := waiter.Session.Transactions.Catalog.Roles
					if private.Settings.Lookup(key)[0].Value != "waiter" || e.RoleStore.Published.Settings.Lookup(key)[0].Value != "holder" {
						t.Fatal("wait failed to refresh immutable predecessor")
					}
				}
			}
			cleanupWaiter()
			if holderActive {
				_, err = e.Rollback(holder, tx)
				holderActive = false
				if err != nil {
					t.Fatal(err)
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("validation_publication_boundary", func(t *testing.T) {
		e := engine.New()
		var interrupt context.CancelFunc
		e.Settings = settings.NewRegistry([]settings.Definition{{
			Name: "database_probe.value", Type: settings.String, Access: settings.Session,
			Normalize: func(value string) (string, *sqlstate.Error, error) {
				if !e.Mu.TryLock() {
					return "", nil, errors.New("normalization under publication mutex")
				}
				e.Mu.Unlock()
				if interrupt != nil {
					interrupt()
				}
				return value, nil, nil
			},
		}}, false)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		op, done, err := enginetest.Operation(e, ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		statement := databaseSettingsStatement(e, "candidate")
		statement.Set.Name.Text = "database_probe.value"
		before, frontier := e.RoleStore.Published, e.RoleStore.NextRevision()
		interrupt = cancel
		_, err = rolecmds.ExecuteDatabaseSettings(e, &core.StatementScope{Op: op}, statement, 0)
		if !errors.Is(err, context.Canceled) || e.RoleStore.Published != before || e.RoleStore.NextRevision() != frontier {
			t.Fatal("cancellation after validation published metadata", err)
		}
		// A later uncanceled execution succeeds; canceling after its linearization
		// cannot roll back the immutable row or retroactively change its result.
		interrupt = nil
		next, nextDone := enginetest.RoleOperation(t, e, "postgres")
		defer nextDone()
		lateCtx, lateCancel := context.WithCancel(next.Context())
		defer lateCancel()
		next.Ctx = lateCtx
		_, err = rolecmds.ExecuteDatabaseSettings(e, &core.StatementScope{Op: next}, statement, 0)
		published := e.RoleStore.Published
		lateCancel()
		key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
		if err != nil || published == before || published != e.RoleStore.Published || published.Settings.Lookup(key)[0].Value != "candidate" {
			t.Fatal("late cancellation relabeled success", err)
		}
	})
}
