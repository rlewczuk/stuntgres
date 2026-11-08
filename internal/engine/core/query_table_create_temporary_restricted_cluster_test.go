package core_test

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func queryTemporaryRestrictedCluster(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, noData := range []bool{false, true} {
			for _, alias := range []bool{false, true} {
				t.Run(fmt.Sprintf("selected-%t/no-data-%t/pg-temp-%t", selected, noData, alias), func(t *testing.T) {
					e := engine.New()
					s := enginetest.StartSession(t, e, nil)
					creation := "CREATE TEMP TABLE IF NOT EXISTS admission_leak AS SELECT 1::integer AS v"
					if alias {
						creation = "CREATE TABLE IF NOT EXISTS pg_temp.admission_leak AS SELECT 1::integer AS v"
					}
					if noData {
						creation += " WITH NO DATA"
					}
					// Only maintenance arms the real volatile side-effect branch.
					for _, q := range []string{
						"CREATE TEMP TABLE admission_sentinel AS SELECT 73::integer AS v",
						"CREATE TABLE public.admission_heap(id INT PRIMARY KEY,k INT,payload TEXT)",
						"INSERT INTO public.admission_heap VALUES(1,3,'c'),(2,1,'a'),(3,2,'b')",
						"SET application_name='ctas setup'",
						"CREATE INDEX admission_plain ON public.admission_heap(k)",
						"CREATE FUNCTION public.admission_effect(integer) RETURNS integer LANGUAGE SQL VOLATILE AS $$SELECT pg_catalog.set_config('application_name','ctas restricted',false); " + creation + "; SELECT $1$$",
						"CREATE FUNCTION public.admission_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT CASE WHEN pg_catalog.current_setting('application_name')='ctas caller' THEN public.admission_effect($1) ELSE $1 END$$",
						"CREATE INDEX admission_expr ON public.admission_heap(public.admission_key(k))",
					} {
						enginetest.SessionSQL(t, s, q)
					}
					for _, d := range e.CatalogRead().Descriptors() {
						if d.Name() == "admission_leak" {
							t.Fatal("inactive index-build branch executed utility body", d)
						}
					}
					address := enginetest.CatalogRef(t, e, e.CatalogRead(), "admission_expr").Address
					originalKey := e.CatalogRead().MustRelation(address).SQLIndex.Keys[0].Derived
					definition := originalKey.Definition.(plan.SQLRoutineExpr)
					execution := originalKey.Execution.(plan.SQLRoutineExpr)
					enginetest.SessionSQL(t, s, "ALTER FUNCTION public.admission_key(integer) VOLATILE")
					currentProcedure, exists := e.CatalogRead().Procedure(definition.Ref.Address)
					if !exists || currentProcedure == definition.Procedure {
						t.Fatal("fixture did not alter same-OID routine declaration")
					}
					enginetest.SessionSQL(t, s, "SET application_name='ctas caller'")
					enginetest.SessionSQL(t, s, "SET search_path=public,pg_catalog")
					parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "admission_heap").Address
					heap := e.Storage.Relations[parent]
					rows := append(heap.Versions[:0:0], heap.Versions...)
					for i := range rows {
						rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
					}
					originalIndex := e.CatalogRead().MustRelation(address)
					root := e.Storage.IndexRoots[address]
					entries := append(root.Entries[:0:0], root.Entries...)
					for i := range entries {
						entries[i].Keys = append([]sqltype.Value(nil), entries[i].Keys...)
					}
					unchanged := queryTemporaryAdmissionRoots(t, e, s)
					op, scope := queryTemporaryScope(t, e, s, context.Background())
					q := "CLUSTER public.admission_heap USING admission_plain"
					if selected {
						q = "CLUSTER public.admission_heap USING admission_expr"
					}
					statement := enginetest.Statement(t, q)
					if err := scope.Gate(statement); err != nil {
						t.Fatal(err)
					}
					values, typed := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed)
					actions, placeholders := maps.Clone(s.Settings.Actions), maps.Clone(s.Settings.Placeholders)
					timers, roleAction, identity := s.Timeouts, s.Settings.RoleAction, op.RoleState()
					_, err := scope.Execute(statement, 0, nil)
					queryTemporaryAdmissionError(t, err, "42501", "cannot create temporary table within security-restricted operation", "", "SQL function \"admission_effect\" statement 2\nSQL function \"admission_key\" statement 2")
					if op.Restricted != nil || !reflect.DeepEqual(op.RoleState(), identity) || !reflect.DeepEqual(s.Settings.Values, values) || !reflect.DeepEqual(s.Settings.Typed, typed) || !reflect.DeepEqual(s.Settings.Actions, actions) || !reflect.DeepEqual(s.Settings.Placeholders, placeholders) || !reflect.DeepEqual(s.Timeouts, timers) || s.Settings.RoleAction != roleAction {
						t.Fatal("restricted maintenance failed to restore caller identity/settings")
					}
					finishErr := scope.Finish(err)
					op.Done()
					queryTemporaryAdmissionError(t, finishErr, "42501", "cannot create temporary table within security-restricted operation", "", "SQL function \"admission_effect\" statement 2\nSQL function \"admission_key\" statement 2")
					unchanged()
					if e.Storage.Relations[parent] != heap || !reflect.DeepEqual(heap.Versions, rows) || e.CatalogRead().MustRelation(address) != originalIndex || e.Storage.IndexRoots[address] != root || !reflect.DeepEqual(root.Entries, entries) {
						t.Fatal("failed maintenance changed original heap/index roots")
					}
					if originalKey.Definition.(plan.SQLRoutineExpr).Procedure != definition.Procedure || originalKey.Definition.(plan.SQLRoutineExpr).Info.RoutineReference != definition.Procedure || originalKey.Execution.(plan.SQLRoutineExpr).Procedure != execution.Procedure || originalKey.Execution.(plan.SQLRoutineExpr).Info.RoutineReference != execution.Procedure {
						t.Fatal("maintenance mutated historical definition/execution carriers")
					}
					for _, d := range e.CatalogRead().Descriptors() {
						if d.Name() == "admission_leak" {
							t.Fatal("restricted CTAS retained relation/type", d)
						}
					}
					// Match the captured declaration before pure-body replacement,
					// then restore VOLATILE for the same existing-index control.
					enginetest.SessionSQL(t, s, "ALTER FUNCTION public.admission_key(integer) IMMUTABLE")
					enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION public.admission_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$")
					enginetest.SessionSQL(t, s, "ALTER FUNCTION public.admission_key(integer) VOLATILE")
					enginetest.SessionSQL(t, s, q)
					if e.Storage.Relations[parent] != heap || !reflect.DeepEqual(heap.Versions, rows) {
						t.Fatal("positive control changed heap rows")
					}
					if current := e.Storage.IndexRoots[address]; current == nil || len(current.Entries) != 3 || !index.ConstructionMatches(current, e.CatalogRead().MustRelation(address)) {
						t.Fatal("positive control did not rebuild actual existing expression index")
					}
					key := e.CatalogRead().MustRelation(address).SQLIndex.Keys[0].Derived
					pure, _ := e.CatalogRead().Procedure(definition.Ref.Address)
					if key == originalKey || key.Definition.(plan.SQLRoutineExpr).Procedure != pure || key.Execution.(plan.SQLRoutineExpr).Procedure != pure {
						t.Fatal("positive control failed to privately refresh current routine carriers")
					}
				})
			}
		}
	}
}
