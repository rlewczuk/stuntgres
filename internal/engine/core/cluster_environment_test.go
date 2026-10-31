package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
)

func TestClusterMaintenanceEnvironment(t *testing.T) {
	for _, kind := range []string{"owner_private", "safe_path", "settings", "restricted", "failure"} {
		for _, selected := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/sibling", true: "/selected"}[selected], func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{
					"CREATE ROLE cluster_env_owner", "CREATE ROLE cluster_env_caller",
					"GRANT USAGE,CREATE ON SCHEMA public TO cluster_env_owner", "GRANT USAGE ON SCHEMA public TO cluster_env_caller",
					"CREATE SCHEMA cluster_env_private AUTHORIZATION cluster_env_owner", "CREATE SCHEMA cluster_env_shadow",
					"CREATE FUNCTION cluster_env_shadow.abs(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 999$$",
					"GRANT USAGE ON SCHEMA cluster_env_shadow TO cluster_env_caller", "SET ROLE cluster_env_owner",
					"CREATE TABLE cluster_env_private.secret(token int)", "INSERT INTO cluster_env_private.secret VALUES(1)",
					"CREATE TABLE public.t(id int,k int,payload text)", "INSERT INTO public.t VALUES(1,3,'c'),(2,1,'a'),(3,2,'b')",
					"CREATE INDEX a ON public.t(k)", "CREATE UNIQUE INDEX u ON public.t(id)",
					"CREATE FUNCTION public.cluster_env_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
					"CREATE INDEX env_idx ON public.t(public.cluster_env_key(k))",
				} {
					enginetest.SessionSQL(t, s, q)
				}
				indexAddress := enginetest.CatalogRef(t, e, e.CatalogRead(), "env_idx").Address
				originalKey := e.CatalogRead().MustRelation(indexAddress).SQLIndex.Keys[0].Derived
				originalDefinition := originalKey.Definition.(plan.SQLRoutineExpr)
				originalExecution := originalKey.Execution.(plan.SQLRoutineExpr)
				body := "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1"
				switch kind {
				case "owner_private":
					body = "SELECT 0; SELECT $1 FROM cluster_env_private.secret WHERE token=1"
				case "safe_path":
					body = "SELECT 0; SELECT $1 / CASE WHEN abs($1)=pg_catalog.abs($1) AND pg_catalog.current_setting('search_path')='pg_catalog, pg_temp' THEN 1 ELSE 0 END"
				case "restricted":
					body = "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT pg_catalog.set_config('role','none',false); SELECT $1"
				case "failure":
					body = "SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1 / CASE WHEN pg_catalog.current_setting('application_name')='cluster local' THEN 0 ELSE 1 END"
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION public.cluster_env_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$"+body+"$$")
				currentProcedure, exists := e.CatalogRead().Procedure(originalDefinition.Ref.Address)
				if !exists || currentProcedure == originalDefinition.Procedure {
					t.Fatal("fixture did not replace the existing routine declaration")
				}
				for _, q := range []string{"RESET ROLE", "GRANT MAINTAIN ON public.t TO cluster_env_caller", "SET ROLE cluster_env_caller", "SET application_name='cluster caller'", "SET search_path=cluster_env_shadow,public,pg_catalog"} {
					enginetest.SessionSQL(t, s, q)
				}
				parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address
				original, catalog := e.Storage.Relations[parent], e.Catalog.State
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				q := "CLUSTER public.t USING a"
				if selected {
					q = "CLUSTER public.t USING env_idx"
				}
				statement := enginetest.Statement(t, q)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				finished := false
				defer func() {
					if !finished {
						_ = scope.Finish(err)
					}
				}()
				if err = scope.Gate(statement); err != nil {
					t.Fatal(err)
				}
				// Admission and Finish legitimately change transaction timers and
				// setting journals. Compare the complete caller image around the
				// actual command work, before that ordinary lifecycle finishes.
				values, typed := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed)
				actions, placeholders := maps.Clone(s.Settings.Actions), maps.Clone(s.Settings.Placeholders)
				timers, roleAction := s.Timeouts, s.Settings.RoleAction
				identity := op.RoleState()
				_, err = scope.Execute(statement, 0, nil)
				if op.Restricted != nil || !reflect.DeepEqual(op.RoleState(), identity) {
					t.Error("maintenance identity/restriction escaped command", op.Restricted, op.RoleState(), identity)
				}
				if !reflect.DeepEqual(s.Settings.Values, values) || !reflect.DeepEqual(s.Settings.Typed, typed) || !reflect.DeepEqual(s.Settings.Actions, actions) || !reflect.DeepEqual(s.Settings.Placeholders, placeholders) || !reflect.DeepEqual(s.Timeouts, timers) || s.Settings.RoleAction != roleAction {
					t.Error("maintenance setting/presentation image escaped command")
				}
				err = scope.Finish(err)
				finished = true
				if originalKey.Definition.(plan.SQLRoutineExpr).Procedure != originalDefinition.Procedure || originalKey.Definition.(plan.SQLRoutineExpr).Info.RoutineReference != originalDefinition.Procedure || originalKey.Execution.(plan.SQLRoutineExpr).Procedure != originalExecution.Procedure || originalKey.Execution.(plan.SQLRoutineExpr).Info.RoutineReference != originalExecution.Procedure {
					t.Fatal("maintenance mutated historical definition/execution carriers")
				}
				if kind == "restricted" || kind == "failure" {
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `cannot set parameter "role" within security-restricted operation`, Context: `SQL function "cluster_env_key" statement 2`}
					if kind == "failure" {
						want.Code, want.Message = "22012", "division by zero"
					}
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
						t.Fatalf("full maintenance diagnostic = %#v (%v), want %#v", diagnostic, err, want)
					}
					if e.Storage.Relations[parent] != original || e.Catalog.State != catalog {
						t.Fatal("failed maintenance published")
					}
				} else {
					if err != nil {
						t.Fatal("actual owner/private/path execution", err)
					}
					currentKey := e.CatalogRead().MustRelation(indexAddress).SQLIndex.Keys[0].Derived
					definition := currentKey.Definition.(plan.SQLRoutineExpr)
					execution := currentKey.Execution.(plan.SQLRoutineExpr)
					if currentKey == originalKey || definition.Procedure != currentProcedure || definition.Info.RoutineReference != currentProcedure || execution.Procedure != currentProcedure || execution.Info.RoutineReference != currentProcedure {
						t.Fatal("maintenance did not privately refresh both current declaration carriers")
					}
					store := e.Storage.Relations[parent]
					if store != original || !reflect.DeepEqual(clusterStorageIDs(store), []int64{1, 2, 3}) {
						t.Fatal("targeted recipe work changed the heap", clusterStorageIDs(store))
					}
					idx := enginetest.CatalogRef(t, e, e.CatalogRead(), "env_idx").Address
					root := e.Storage.IndexRoots[idx]
					if root == nil || len(root.Entries) != 3 {
						t.Fatal("missing actual routine sibling", root)
					}
					for _, entry := range root.Entries {
						row := store.Versions[entry.Position]
						if row.Row != entry.Row || row.Version != entry.Version || enginetest.Export(entry.Keys[0]) != enginetest.Export(row.Values[1]) {
							t.Fatal("environment produced incorrect key authority", entry, row)
						}
					}
				}
			})
		}
	}
	t.Run("cancellation_restores_environment", func(t *testing.T) {
		e, s, parent := clusterStorageSeed(t)
		for _, q := range []string{
			"CREATE FUNCTION public.cluster_env_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
			"CREATE INDEX env_idx ON t(public.cluster_env_key(k))",
			"CREATE OR REPLACE FUNCTION public.cluster_env_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT pg_catalog.set_config('application_name','cluster local',false); SELECT $1$$",
			"SET application_name='cluster caller'", "SET search_path=public,pg_catalog",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		original, catalog := e.Storage.Relations[parent], e.Catalog.State
		pause := util.NewExecutionPause("index.overlay.build")
		defer pause.Resume()
		ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		statement := enginetest.Statement(t, "CLUSTER t USING env_idx")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = scope.Finish(err)
			}
		}()
		if err = scope.Gate(statement); err != nil {
			t.Fatal(err)
		}
		// Observe exact maintenance restoration after admission, but before
		// cancellation retires the command's normal transaction timer state.
		values, typed := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed)
		actions, placeholders := maps.Clone(s.Settings.Actions), maps.Clone(s.Settings.Placeholders)
		timers, roleAction := s.Timeouts, s.Settings.RoleAction
		identity := op.RoleState()
		done := make(chan error, 1)
		go func() { _, err := scope.Execute(statement, 0, nil); done <- err }()
		joined := false
		defer func() {
			cancel()
			pause.Resume()
			if !joined {
				err = <-done
			}
		}()
		if err = pause.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		err = <-done
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("actual expression-work cancellation", err)
		}
		if op.Restricted != nil || !reflect.DeepEqual(op.RoleState(), identity) || !reflect.DeepEqual(s.Settings.Values, values) || !reflect.DeepEqual(s.Settings.Typed, typed) || !reflect.DeepEqual(s.Settings.Actions, actions) || !reflect.DeepEqual(s.Settings.Placeholders, placeholders) || !reflect.DeepEqual(s.Timeouts, timers) || s.Settings.RoleAction != roleAction {
			t.Fatal("cancellation leaked maintenance environment")
		}
		err = scope.Finish(err)
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("finishing canceled expression work", err)
		}
		if e.Storage.Relations[parent] != original || e.Catalog.State != catalog {
			t.Fatal("canceled expression work published")
		}
	})
}
