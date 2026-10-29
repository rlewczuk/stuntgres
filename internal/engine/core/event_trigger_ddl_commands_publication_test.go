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
	"stuntgres/internal/sqltype"
)

func TestEventTriggerDDLCommandsPublication(t *testing.T) {
	for _, finish := range []string{"cancel", "finish_veto"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE et_dc_publish_old(n integer)",
				"INSERT INTO et_dc_publish_old VALUES(7)",
				"CREATE TABLE et_dc_publish_audit(classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
				"INSERT INTO et_dc_publish_audit VALUES(NULL,NULL,NULL,'retained','sentinel',NULL,NULL,false,false)",
				"CREATE FUNCTION et_dc_publish_end() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_publish_audit SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_dc_publish_new VALUES(8); END$$",
				"CREATE EVENT TRIGGER et_dc_publish_end ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION et_dc_publish_end()",
			} {
				enginetest.SessionSQL(t, actor, q)
			}
			before := e.CatalogRead()
			target := enginetest.RoleTableRef(t, e, "et_dc_publish_old")
			audit := enginetest.RoleTableRef(t, e, "et_dc_publish_audit")
			original := before.MustRelation(target.Address)
			oldKey := ids.NameKey{ClassID: ids.RelationClassID, Namespace: original.Namespace(), Name: "et_dc_publish_old"}
			newKey := oldKey
			newKey.Name = "et_dc_publish_new"
			frontier, roles := e.Visibility.CommitSeq(), e.RoleStore.Published
			targetStore, auditStore := e.Storage.Relations[target.Address], e.Storage.Relations[audit.Address]
			targetRoot, auditRoot := targetStore.Versions, auditStore.Versions
			targetEnds, auditEnds := targetStore.Ends, auditStore.Ends
			targetGeneration, auditGeneration := targetStore.Generation, auditStore.Generation
			wantTarget := [][]driver.Value{{int64(7)}}
			wantAudit := [][]driver.Value{{nil, nil, nil, "retained", "sentinel", nil, nil, false, false}}
			exportRows := func(rows []storage.RowVersion) [][]driver.Value {
				values := make([][]driver.Value, len(rows))
				for i, row := range rows {
					values[i] = make([]driver.Value, len(row.Values))
					for j, value := range row.Values {
						if !value.IsNull() && value.Type().ID() == sqltype.Oid {
							oid, err := value.OID()
							if err != nil {
								t.Fatal(err)
							}
							values[i][j] = int64(oid)
						} else {
							values[i][j] = enginetest.Export(value)
						}
					}
				}
				return values
			}
			assertRetained := func() {
				t.Helper()
				if before.Get(target.Address) != original || original.Name() != oldKey.Name || before.NameAddress(oldKey) != target.Address || before.NameAddress(newKey) != (ids.ObjectAddress{}) {
					t.Fatal("retained catalog descriptor/name root mutated")
				}
				if !reflect.DeepEqual(exportRows(targetRoot), wantTarget) || !reflect.DeepEqual(exportRows(auditRoot), wantAudit) {
					t.Fatal("retained committed row backing mutated")
				}
			}
			assertCommitted := func() {
				t.Helper()
				assertRetained()
				if e.CatalogRead().State() != before.State() || e.RoleStore.Published != roles || e.Visibility.CommitSeq() != frontier {
					t.Fatal("rejected command published catalog/roles/visibility frontier")
				}
				if e.Storage.Relations[target.Address] != targetStore || e.Storage.Relations[audit.Address] != auditStore || targetStore.Generation != targetGeneration || auditStore.Generation != auditGeneration || targetStore.Ends != targetEnds || auditStore.Ends != auditEnds || !storage.SameVersionRoot(targetStore.Versions, targetRoot) || !storage.SameVersionRoot(auditStore.Versions, auditRoot) {
					t.Fatal("rejected command changed committed heap incarnation/row/end roots")
				}
			}
			wantProduced := [][]driver.Value{{int64(1259), int64(target.Address.ObjectID), int64(0), "ALTER TABLE", "table", "public", "public.et_dc_publish_new", false, true}}
			q := "ALTER TABLE et_dc_publish_old RENAME TO et_dc_publish_new"
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
				defer scope.Finish(errors.New("unfinished helper publication witness"))
				// Execute owns the real ALTER producer and invokes the PL/pgSQL end
				// callback. No command scratch or audit rows are installed by the test.
				if _, err := scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
					t.Fatal(err)
				}
				if scope.Catalog == nil || scope.Completed || scope.EventCommand != nil {
					t.Fatal("helper callback escaped private Finish ownership or retained lexical state")
				}
				private := e.PrivateCatalogLocked(scope.PendingCatalog())
				next, _ := private.Get(target.Address).(*catalog.Relation)
				if next == nil || next == original || next.Name() != newKey.Name || next.Revision() <= original.Revision() || private.NameAddress(oldKey) != (ids.ObjectAddress{}) || private.NameAddress(newKey) != target.Address {
					t.Fatal("genuine ALTER rename missing from private catalog/name root")
				}
				if !reflect.DeepEqual(exportRows(scope.Stage.Rows[audit.Address]), wantProduced) || !reflect.DeepEqual(exportRows(scope.Stage.Rows[target.Address]), [][]driver.Value{{int64(8)}}) || len(scope.Stage.Rows) != 2 || len(scope.Stage.Deletes) != 0 {
					t.Fatal("missing exact helper-derived audit and callback row effects", exportRows(scope.Stage.Rows[audit.Address]), exportRows(scope.Stage.Rows[target.Address]))
				}
				assertCommitted()
				veto := errors.New("helper command terminal Finish veto")
				var primary error
				want := error(context.Canceled)
				if finish == "cancel" {
					// Cancellation starts only after the real helper audit and rename
					// effects have been observed in the private journal/stage.
					cancel()
				} else {
					primary, want = veto, veto
				}
				if err := scope.Finish(primary); err != want {
					t.Fatal("real Finish lost cancellation or terminal veto", err)
				}
				assertCommitted()
				if !scope.Finished || scope.EventCommand != nil || scope.View != nil || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.OwnWrites != nil || scope.OwnDeletes != nil || core.ActiveScopeOfLease(op.Lease) != nil || core.OperationCatalog(op) != op.Session.Transactions.Catalog {
					t.Fatal("rejected Finish retained live statement/stage authority")
				}
				e.Mu.Lock()
				auth, authErr := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if authErr != nil || auth.Statement != nil || auth.Catalog.State() != before.State() {
					t.Fatal("post-Finish authorization retained rejected private catalog", authErr)
				}
			}()
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT n FROM et_dc_publish_old ORDER BY n")); !reflect.DeepEqual(got, wantTarget) {
				t.Fatal("rejected callback changed committed target rows", got)
			}
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT * FROM et_dc_publish_audit")); !reflect.DeepEqual(got, wantAudit) {
				t.Fatal("rejected helper audit became visible", got)
			}
			// Retry the identical real command on the same session: its name claim,
			// callback, helper source and write stage must all remain usable.
			enginetest.SessionSQL(t, actor, q)
			installed := e.CatalogRead()
			if installed.State() == before.State() || installed.NameAddress(oldKey) != (ids.ObjectAddress{}) || installed.NameAddress(newKey) != target.Address || e.Visibility.CommitSeq() <= frontier {
				t.Fatal("healthy retry did not publish rename/frontier")
			}
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT n FROM et_dc_publish_new ORDER BY n")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}, {int64(8)}}) {
				t.Fatal("healthy retry lost or duplicated callback rows", got)
			}
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT classid::bigint,objid::bigint,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_publish_audit WHERE present")); !reflect.DeepEqual(got, wantProduced) {
				t.Fatal("healthy retry did not publish exactly the genuine helper audit", got)
			}
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_dc_publish_audit")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
				t.Fatal("healthy retry replaced retained audit prefix or replayed rejected suffix", got)
			}
			assertRetained()
		})
	}
}
