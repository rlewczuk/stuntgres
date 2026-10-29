package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestQueryTableRoutinePublication(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "finish_veto", "scratch_only"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE qt_calls(n bigint)",
				"INSERT INTO qt_calls VALUES(99)",
				"CREATE TABLE qt_bag(command_tag text)",
				"CREATE TABLE qt_callbacks(tag text)",
				"CREATE FUNCTION qt_read() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.qt_calls SELECT count(*) FROM pg_event_trigger_ddl_commands(); INSERT INTO public.qt_bag SELECT command_tag FROM pg_event_trigger_ddl_commands(); RETURN 7; END$$",
				"CREATE FUNCTION qt_end() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.qt_callbacks VALUES(TG_TAG); END$$",
				"CREATE EVENT TRIGGER qt_end ON ddl_command_end WHEN TAG IN ('CREATE VIEW') EXECUTE FUNCTION qt_end()",
			} {
				enginetest.SessionSQL(t, actor, q)
			}
			before := e.CatalogRead()
			calls := enginetest.RoleTableRef(t, e, "qt_calls")
			original := before.MustRelation(calls.Address)
			store := e.Storage.Relations[calls.Address]
			root, ends, generation := store.Versions, store.Ends, store.Generation
			frontier, roles := e.Visibility.CommitSeq(), e.RoleStore.Published
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: original.Namespace(), Name: "qt_result"}
			wantRetained := [][]driver.Value{{int64(99)}}
			exportRows := func(rows []storage.RowVersion) [][]driver.Value {
				values := make([][]driver.Value, len(rows))
				for i, row := range rows {
					values[i] = make([]driver.Value, len(row.Values))
					for j, value := range row.Values {
						values[i][j] = enginetest.Export(value)
					}
				}
				return values
			}
			assertRetained := func() {
				t.Helper()
				if before.Get(calls.Address) != original || before.NameAddress(key) != (ids.ObjectAddress{}) || !reflect.DeepEqual(exportRows(root), wantRetained) {
					t.Fatal("retained catalog/name/row backing changed")
				}
			}
			assertUnpublished := func() {
				t.Helper()
				assertRetained()
				if e.CatalogRead().State() != before.State() || e.RoleStore.Published != roles || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[calls.Address] != store || store.Generation != generation || store.Ends != ends || !storage.SameVersionRoot(store.Versions, root) {
					t.Fatal("CTAS or ordinary function effects published before successful Finish")
				}
			}
			query := "CREATE TABLE qt_result AS SELECT public.qt_read() AS i"
			if outcome == "scratch_only" {
				query = "CREATE TABLE qt_result AS SELECT 7 AS i"
			}
			func() {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := actor.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished CTAS routine publication witness"))
				result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
				if err != nil || result.Count != 1 || result.Rows != nil || scope.EventCommand != nil {
					t.Fatal("genuine CTAS failed or retained lexical collector", result, err)
				}
				if outcome == "scratch_only" {
					if scope.Catalog != nil || !scope.Completed || len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 || e.CatalogRead().NameAddress(key) == (ids.ObjectAddress{}) {
						t.Fatal("unmatched inventory alone forced a callback journal or delayed ordinary CTAS")
					}
					if err := scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					return
				}
				if scope.Catalog == nil || scope.Completed {
					t.Fatal("ordinary function effects did not retain destination Finish ownership")
				}
				e.Mu.Lock()
				private := e.PrivateCatalogLocked(scope.PendingCatalog())
				created := private.NameAddress(key)
				destination, _ := private.Get(created).(*catalog.Relation)
				privateStore := e.PrivateStorageLocked(scope.PendingCatalog(), created)
				e.Mu.Unlock()
				if created == (ids.ObjectAddress{}) || destination == nil || destination.Name() != key.Name || privateStore == nil || e.CatalogRead().Get(created) != nil || e.Storage.Relations[created] != nil {
					t.Fatal("destination catalog/name/storage did not remain private")
				}
				if !reflect.DeepEqual(exportRows(scope.Stage.Rows[created]), [][]driver.Value{{int64(7)}}) || !reflect.DeepEqual(exportRows(scope.Stage.Rows[calls.Address]), [][]driver.Value{{int64(0)}}) || len(scope.Stage.Rows) != 2 || len(scope.Stage.Deletes) != 0 {
					t.Fatal("root stage lost genuine function INSERT or CTAS value", exportRows(scope.Stage.Rows[calls.Address]), exportRows(scope.Stage.Rows[created]))
				}
				assertUnpublished()
				var primary, want error
				if outcome == "cancel" {
					cancel()
					want = context.Canceled
				} else if outcome == "finish_veto" {
					primary = errors.New("CTAS ordinary function terminal veto")
					want = primary
				}
				if err := scope.Finish(primary); err != want {
					t.Fatal("actual Finish lost terminal outcome", err, want)
				}
				if !scope.Finished || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.View != nil || scope.OwnWrites != nil || scope.OwnDeletes != nil || core.ActiveScopeOfLease(op.Lease) != nil || core.OperationCatalog(op) != op.Session.Transactions.Catalog {
					t.Fatal("Finish retained statement/stage authority")
				}
				if want != nil {
					assertUnpublished()
					if e.CatalogRead().Get(created) != nil || e.Storage.Relations[created] != nil {
						t.Fatal("rejected destination became committed")
					}
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					e.Mu.Unlock()
					if err != nil || auth.Statement != nil || auth.Catalog.State() != before.State() {
						t.Fatal("rejected Finish retained private catalog authority", err)
					}
				} else if e.CatalogRead().NameAddress(key) != created || e.Storage.Relations[created] == nil || e.Visibility.CommitSeq() <= frontier {
					t.Fatal("successful Finish did not publish destination/frontier")
				}
			}()
			if outcome == "cancel" || outcome == "finish_veto" {
				// Reuse the same session and destination name after real rejection.
				enginetest.SessionSQL(t, actor, query)
			}
			wantCalls := [][]driver.Value{{int64(0)}, {int64(99)}}
			if outcome == "scratch_only" {
				wantCalls = wantRetained
			}
			for _, check := range []struct {
				query string
				want  [][]driver.Value
			}{
				{"SELECT n FROM qt_calls ORDER BY n", wantCalls},
				{"SELECT i FROM qt_result", [][]driver.Value{{int64(7)}}},
				{"SELECT count(*) FROM qt_bag", [][]driver.Value{{int64(0)}}},
				{"SELECT count(*) FROM qt_callbacks", [][]driver.Value{{int64(0)}}},
			} {
				if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, check.query)); !reflect.DeepEqual(got, check.want) {
					t.Fatal("healthy publication lost/duplicated effects or invoked unmatched callback", check.query, got)
				}
			}
			assertRetained()
		})
	}
}
