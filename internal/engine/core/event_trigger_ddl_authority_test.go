package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"testing"
)

func TestEventTriggerDDLFiringAuthority(t *testing.T) {
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE et_auth_data(v integer)", "CREATE TABLE et_auth_audit(v integer)", "CREATE FUNCTION et_auth_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_auth_audit VALUES(1); SELECT 1$$", "CREATE FUNCTION et_auth_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_auth_record() INTO n; END$$", "CREATE EVENT TRIGGER et_auth ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION et_auth_fn()"} {
		enginetest.SessionSQL(t, actor, q)
	}
	ast, held := sequencePreparedDescription(t, e, actor, "ALTER TABLE et_auth_data ALTER COLUMN v SET DEFAULT 7")
	query, prepared := sequencePreparedDescription(t, e, actor, "SELECT COALESCE(-(7::integer),0) FROM et_auth_data")
	execute := func(want int64) {
		t.Helper()
		r, err := sequencePreparedExecute(e, actor, ast, &held)
		if err != nil {
			t.Fatal(err)
		}
		if r.Rows != nil {
			r.Rows.Close()
		}
		got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_auth_audit"))
		if !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
			t.Fatal("effective authority", got, want)
		}
		previous := prepared.PlanGeneration
		r, err = sequencePreparedExecute(e, actor, query, &prepared)
		if err != nil {
			t.Fatal(err)
		}
		if r.Rows != nil {
			r.Rows.Close()
		}
		if prepared.PlanOwner != actor.SessionToken || prepared.PlanGeneration != actor.DiscardPlansGeneration || prepared.PlanGeneration < previous {
			t.Fatal("held source retained stale generation")
		}
		if held.Analysis != nil || held.Folds != nil || len(held.Results) != 0 {
			t.Fatal("utility retained borrowed execution artifacts")
		}
	}
	var count int64
	for _, tc := range []struct {
		mode, role string
		fires      bool
	}{{"ENABLE REPLICA", "origin", false}, {"ENABLE REPLICA", "replica", true}, {"ENABLE", "replica", false}, {"ENABLE", "local", true}, {"ENABLE ALWAYS", "origin", true}, {"ENABLE ALWAYS", "replica", true}, {"DISABLE", "replica", false}} {
		enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_auth "+tc.mode)
		enginetest.SessionSQL(t, actor, "SET session_replication_role="+tc.role)
		if tc.fires {
			count++
		}
		execute(count)
		if len(held.Parameters.Types) != 0 {
			t.Fatal("utility acquired parameters")
		}
	}
	for _, q := range []string{"ALTER EVENT TRIGGER et_auth ENABLE REPLICA", "SET session_replication_role=origin", "BEGIN", "SET LOCAL session_replication_role=replica", "SAVEPOINT et_auth_save", "SET LOCAL session_replication_role=local"} {
		enginetest.SessionSQL(t, actor, q)
	}
	execute(count)
	enginetest.SessionSQL(t, actor, "ROLLBACK TO et_auth_save")
	count++
	execute(count)
	enginetest.SessionSQL(t, actor, "COMMIT")
	execute(count)
	// Matching R/A opens a real journal and retains callback rows until Finish;
	// nonmatching roles retain ordinary completed utility ownership instead.
	for _, mode := range []string{"ENABLE REPLICA", "ENABLE ALWAYS"} {
		enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_auth "+mode)
		enginetest.SessionSQL(t, actor, "SET session_replication_role=replica")
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
			defer scope.Finish(errors.New("unpublished authority witness"))
			before := e.CatalogRead()
			if _, err = scope.Execute(ast, 0, nil); err != nil {
				t.Fatal(err)
			}
			if scope.Catalog == nil || scope.Completed || scope.EventCommand != nil || e.CatalogRead().State() != before.State() {
				t.Fatal("matching role bypassed real journal")
			}
		}()
	}
}

func TestEventTriggerDDLDropCapture(t *testing.T) {
	for _, mode := range []struct {
		sql  string
		mode catalog.TriggerMode
	}{{"ENABLE", catalog.TriggerOrigin}, {"DISABLE", catalog.TriggerDisabled}, {"ENABLE REPLICA", catalog.TriggerReplica}, {"ENABLE ALWAYS", catalog.TriggerAlways}} {
		for _, role := range []string{"origin", "local", "replica"} {
			t.Run(mode.sql+"/"+role, func(t *testing.T) {
				e := engine.New()
				actor := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE et_capture_data(v integer)", "CREATE TABLE et_capture_audit(kind text,identity text)", "CREATE FUNCTION et_capture_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_capture_audit SELECT object_type,object_identity FROM pg_event_trigger_dropped_objects() WHERE object_type='table'; END$$", "CREATE EVENT TRIGGER et_capture ON sql_drop EXECUTE FUNCTION et_capture_fn()", "ALTER EVENT TRIGGER et_capture " + mode.sql, "SET session_replication_role=" + role} {
					enginetest.SessionSQL(t, actor, q)
				}
				base := e.CatalogRead()
				ref := enginetest.RoleTableRef(t, e, "et_capture_data")
				if catalog.CollectDroppedObjects(base) != (mode.mode != catalog.TriggerDisabled) {
					t.Fatal("catalog admission must be role-independent")
				}
				stage := e.BeginCatalogStage(base)
				stage.MarkDrop(ref.Address, catalog.DropProvenance{Original: true})
				if (len(stage.DropFlags) != 0) != (mode.mode != catalog.TriggerDisabled) {
					t.Fatal("scope-less mark dropped replica superset")
				}
				e.DiscardCatalogStage(stage)
				op, err := actor.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished drop capture"))
				if _, err = scope.Execute(enginetest.Statement(t, "DROP TABLE et_capture_data"), 0, nil); err != nil {
					t.Fatal(err)
				}
				fires := mode.mode.Fires(role == "replica")
				if fires {
					if scope.Catalog == nil || scope.Completed {
						t.Fatal("matching sql_drop lacks journal")
					}
					audit := enginetest.RoleTableRef(t, e, "et_capture_audit")
					if len(scope.Stage.Rows[audit.Address]) != 1 {
						t.Fatal("missing real pre-drop table record")
					}
				} else if scope.Catalog != nil || scope.EventCommand != nil || !scope.Completed {
					t.Fatal("role-disabled sql_drop allocated command journal")
				}
				if err = scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT kind,identity FROM et_capture_audit"))
				var want [][]driver.Value
				if fires {
					want = [][]driver.Value{{"table", "public.et_capture_data"}}
				}
				if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
					t.Fatal(got, want)
				}
			})
		}
	}
}
