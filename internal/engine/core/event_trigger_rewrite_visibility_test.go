package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func eventTriggerRewriteVisibility(t *testing.T) {
	for _, typed := range []bool{false, true} {
		for _, mode := range []string{"commit", "callback_abort", "finish_veto", "private_successor"} {
			t.Run(fmt.Sprintf("typed=%v/%s", typed, mode), func(t *testing.T) {
				e := engine.New()
				actor := enginetest.StartSession(t, e, nil)
				setup := []string{"CREATE TABLE et_visibility_parent(v integer)", "CREATE TABLE et_visibility_child() INHERITS(et_visibility_parent)"}
				names := "'et_visibility_parent','et_visibility_child'"
				wantMetadata := 2
				command, tag := "ALTER TABLE et_visibility_parent ALTER COLUMN v TYPE bigint", "ALTER TABLE"
				if typed {
					setup = []string{"CREATE TYPE et_visibility_type AS(v integer)", "CREATE TABLE et_visibility_parent OF et_visibility_type", "CREATE TABLE et_visibility_child OF et_visibility_type"}
					names += ",'et_visibility_type'"
					wantMetadata = 3
					command, tag = "ALTER TYPE et_visibility_type ALTER ATTRIBUTE v TYPE bigint CASCADE", "ALTER TYPE"
				}
				for _, q := range setup {
					enginetest.SessionSQL(t, actor, q)
				}
				for _, q := range []string{
					"CREATE INDEX et_visibility_parent_ix ON et_visibility_parent(v)", "CREATE INDEX et_visibility_child_ix ON et_visibility_child(v)",
					"INSERT INTO et_visibility_parent VALUES(1)", "INSERT INTO et_visibility_child VALUES(7)",
					"CREATE TABLE et_visibility_audit(n integer)",
					"CREATE TABLE et_visibility_side(v integer)", "INSERT INTO et_visibility_side VALUES(42)",
					"CREATE FUNCTION et_visibility_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_visibility_audit VALUES(1); SELECT 1$$",
					"CREATE FUNCTION et_visibility_marker() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_visibility_kept(id integer); ALTER TABLE et_visibility_side ADD COLUMN kept integer DEFAULT 9; SELECT 1$$",
				} {
					enginetest.SessionSQL(t, actor, q)
				}
				body := fmt.Sprintf("DECLARE n integer; BEGIN SELECT count(*) INTO n FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relname IN (%s) AND a.attname='v' AND a.atttypid=20; IF n<>%d THEN RAISE EXCEPTION 'metadata phase incomplete'; END IF; SELECT et_visibility_record() INTO n; SELECT count(*) INTO n FROM pg_class WHERE relname='et_visibility_kept'; IF n=0 THEN SELECT et_visibility_marker() INTO n; END IF;", names, wantMetadata)
				body += " IF pg_event_trigger_table_rewrite_reason()<>4 OR pg_event_trigger_table_rewrite_oid() NOT IN ('et_visibility_parent'::regclass,'et_visibility_child'::regclass) THEN RAISE EXCEPTION 'wrong live rewrite pair'; END IF;"
				if mode == "callback_abort" {
					body += " RAISE EXCEPTION 'visibility callback abort';"
				}
				if mode == "private_successor" {
					// The genuine nested ALTER must not be erased by replaying an old
					// metadata stage. Its private predecessor mismatch aborts the root.
					enginetest.SessionSQL(t, actor, "CREATE FUNCTION et_visibility_mutate() RETURNS integer LANGUAGE SQL AS $$ALTER TABLE et_visibility_parent ALTER COLUMN v SET DEFAULT 9; SELECT 1$$")
					body += " SELECT et_visibility_mutate() INTO n;"
				}
				enginetest.SessionSQL(t, actor, "CREATE FUNCTION et_visibility_rewrite() RETURNS event_trigger LANGUAGE plpgsql AS $$"+body+" END$$")
				enginetest.SessionSQL(t, actor, "CREATE EVENT TRIGGER et_visibility_rewrite ON table_rewrite WHEN TAG IN ('"+tag+"') EXECUTE FUNCTION et_visibility_rewrite()")
				before, frontier := e.Catalog.State, e.Visibility.CommitSeq()
				type physicalSnapshot struct {
					ref        ids.ObjectRef
					store      *storage.Relation
					generation uint64
					values     []sqltype.Value
				}
				var physical [2]physicalSnapshot
				for i, name := range []string{"et_visibility_parent", "et_visibility_child"} {
					ref := enginetest.RoleTableRef(t, e, name)
					store := e.Storage.Relations[ref.Address]
					physical[i] = physicalSnapshot{ref, store, store.Generation, slices.Clone(store.Versions[0].Values)}
				}
				audit := enginetest.RoleTableRef(t, e, "et_visibility_audit")
				auditStore := e.Storage.Relations[audit.Address]
				side := enginetest.RoleTableRef(t, e, "et_visibility_side")
				sideStore := e.Storage.Relations[side.Address]
				marker := ids.NameKey{ClassID: ids.RelationClassID, Namespace: before.MustRelation(audit.Address).Namespace(), Name: "et_visibility_kept"}
				func() {
					op, err := actor.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(errors.New("unfinished rewrite visibility"))
					_, executionErr := scope.Execute(enginetest.Statement(t, command), 0, nil)
					if scope.EventCommand != nil {
						t.Fatal("completed utility retained lexical rewrite context", scope.EventCommand)
					}
					wantFailure := mode == "callback_abort" || mode == "private_successor"
					if (executionErr != nil) != wantFailure {
						t.Fatal("unexpected real rewrite outcome", executionErr)
					}
					if e.Catalog.State != before || e.Visibility.CommitSeq() != frontier || len(auditStore.Versions) != 0 || before.NameAddress(marker) != (ids.ObjectAddress{}) {
						t.Fatal("metadata/rows/frontier escaped private phase")
					}
					if e.Storage.Relations[side.Address] != sideStore || len(sideStore.Versions[0].Values) != 1 {
						t.Fatal("nested callback ALTER rewrote committed side storage")
					}
					if mode == "private_successor" {
						if scope.Catalog == nil {
							t.Fatal("missing real callback successor journal")
						}
						next := e.PrivateCatalogLocked(scope.Catalog).MustRelation(physical[0].ref.Address)
						if _, present := next.ColumnMeta.Defaults[next.Attributes[0]]; !present {
							t.Fatal("failure did not reach actual private source mutation")
						}
					}
					for _, source := range physical {
						if e.Storage.Relations[source.ref.Address] != source.store || source.store.Generation != source.generation || !reflect.DeepEqual(source.store.Versions[0].Values, source.values) || before.MustRelation(source.ref.Address).Columns[0].Usage.Type.ID() != sqltype.Int4 {
							t.Fatal("old physical/catalog source was mutated before Finish")
						}
						if executionErr == nil {
							private := e.PrivateCatalogLocked(scope.Catalog)
							store := e.PrivateStorageLocked(scope.Catalog, source.ref.Address)
							if private.MustRelation(source.ref.Address).Columns[0].Usage.Type.ID() != sqltype.Int8 || store == source.store || store.Generation <= source.generation {
								t.Fatal("metadata-first continuation did not retain genuine rewritten successor")
							}
						}
					}
					if executionErr == nil && (scope.Completed || len(scope.Stage.Rows[audit.Address]) != 2 || e.PrivateCatalogLocked(scope.Catalog).NameAddress(marker) == (ids.ObjectAddress{})) {
						t.Fatal("callback effects or Finish ownership were lost")
					}
					failure := executionErr
					if mode == "finish_veto" {
						failure = errors.New("visibility terminal veto")
					}
					if err := scope.Finish(failure); err != failure {
						t.Fatal("Finish changed the primary outcome", err)
					}
				}()
				wantRows, wantType := int64(0), sqltype.Int4
				if mode == "commit" {
					wantRows, wantType = 2, sqltype.Int8
				} else if e.Catalog.State != before || e.Visibility.CommitSeq() != frontier {
					t.Fatal("failed command installed a metadata or storage prefix")
				}
				sideQuery := "SELECT v FROM et_visibility_side"
				sideWant := [][]driver.Value{{int64(42)}}
				if mode == "commit" {
					sideQuery = "SELECT v,kept FROM et_visibility_side"
					sideWant = [][]driver.Value{{int64(42), int64(9)}}
				}
				if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, sideQuery)); !reflect.DeepEqual(got, sideWant) {
					t.Fatal("nested ALTER rows escaped or disappeared", got)
				}
				if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_visibility_audit")); !reflect.DeepEqual(got, [][]driver.Value{{wantRows}}) {
					t.Fatal("callback rows did not follow root outcome", got)
				}
				for i, source := range physical {
					if e.CatalogRead().MustRelation(source.ref.Address).Columns[0].Usage.Type.ID() != wantType {
						t.Fatal("wrong final declared type")
					}
					q := fmt.Sprintf("SELECT v FROM ONLY %s WHERE v=%d", e.CatalogRead().MustRelation(source.ref.Address).Name(), []int64{1, 7}[i])
					if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, q)); !reflect.DeepEqual(got, [][]driver.Value{{[]int64{1, 7}[i]}}) {
						t.Fatal("rewritten/restored indexed data lost", got)
					}
				}
				if mode != "commit" && e.CatalogRead().NameAddress(marker) != (ids.ObjectAddress{}) {
					t.Fatal("callback catalog mutation survived aborted root")
				}
			})
		}
	}
}
