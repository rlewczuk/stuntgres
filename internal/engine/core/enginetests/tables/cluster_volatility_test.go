package tables_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestClusterExistingIndexVolatility(t *testing.T) {
	for _, volatility := range []string{"STABLE", "VOLATILE"} {
		for _, kind := range []string{"selected_expression", "sibling_expression", "sibling_predicate"} {
			for _, commit := range []bool{false, true} {
				name := volatility + "/" + kind + map[bool]string{false: "/rollback", true: "/commit"}[commit]
				t.Run(name, func(t *testing.T) {
					e, s, parent := clusterStorageSeed(t)
					result, body := "integer", "SELECT 0; SELECT $1+10"
					suffix := "ON t(public.cluster_volatility_key(k))"
					message := "functions in index expression must be marked IMMUTABLE"
					if kind == "sibling_predicate" {
						result, body = "boolean", "SELECT 0; SELECT $1>1"
						suffix = "ON t(k) WHERE public.cluster_volatility_key(k)"
						message = "functions in index predicate must be marked IMMUTABLE"
					}
					enginetest.SessionSQL(t, s, "CREATE FUNCTION public.cluster_volatility_key(integer) RETURNS "+result+" LANGUAGE SQL IMMUTABLE AS $$"+body+"$$")
					enginetest.SessionSQL(t, s, "CREATE INDEX v "+suffix)
					ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "v")
					originalIndex := e.CatalogRead().MustRelation(ref.Address)
					var originalDefinition, originalExecution expr.Expr
					if kind == "sibling_predicate" {
						originalDefinition, originalExecution = originalIndex.SQLIndex.Predicate.Definition, originalIndex.SQLIndex.Predicate.Execution
					} else {
						originalDefinition, originalExecution = originalIndex.SQLIndex.Keys[0].Derived.Definition, originalIndex.SQLIndex.Keys[0].Derived.Execution
					}
					originalCall := originalDefinition.(plan.SQLRoutineExpr)
					if exec.PlannedVolatility(originalExecution) != expr.Immutable {
						t.Fatal("fixture is not an immutable existing recipe")
					}
					enginetest.SessionSQL(t, s, "ALTER FUNCTION public.cluster_volatility_key(integer) "+volatility)
					procedure, exists := e.CatalogRead().Procedure(originalCall.Ref.Address)
					if !exists || procedure == originalCall.Procedure || procedure.Signature != originalCall.Procedure.Signature {
						t.Fatal("fixture did not alter the same routine identity")
					}
					before, catalog := e.Storage.Relations[parent], e.Catalog.State
					rows := append([]storage.RowVersion(nil), before.Versions...)
					roots := maps.Clone(e.Storage.IndexRoots)
					// Creation still rejects the changed declaration, independently of
					// whether rebuilding an already admitted index is permitted.
					op, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, enginetest.Statement(t, "CREATE INDEX rejected_idx "+suffix), 0, nil)
					op.Done()
					var diagnostic *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: message}
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
						t.Fatalf("new declaration diagnostic = %#v (%v), want %#v", diagnostic, err, want)
					}
					if e.Catalog.State != catalog || e.Storage.Relations[parent] != before || !maps.Equal(e.Storage.IndexRoots, roots) {
						t.Fatal("failed new declaration leaked publication")
					}
					selected := "a"
					if kind == "selected_expression" {
						selected = "v"
					}
					q := "CLUSTER t USING " + selected
					enginetest.SessionSQL(t, s, "BEGIN")
					enginetest.SessionSQL(t, s, q)
					if commit {
						enginetest.SessionSQL(t, s, "COMMIT")
					} else {
						enginetest.SessionSQL(t, s, "ROLLBACK")
						if e.Catalog.State != catalog || e.Storage.Relations[parent] != before || !maps.Equal(e.Storage.IndexRoots, roots) {
							t.Fatal("rollback retained a refreshed recipe, heap or root")
						}
						enginetest.SessionSQL(t, s, q)
					}
					after := e.Storage.Relations[parent]
					if after != before || !reflect.DeepEqual(after.Versions, rows) {
						t.Fatal("targeted recipe maintenance changed the heap")
					}
					byRow := make(map[ids.RowID]storage.RowVersion, len(rows))
					for _, row := range rows {
						byRow[row.Row] = row
					}
					for _, row := range after.Versions {
						old := byRow[row.Row]
						if row.Version != old.Version || row.Born != old.Born || row.InsertFullXID != old.InsertFullXID || row.KeyChangedAt != old.KeyChangedAt || !reflect.DeepEqual(row.Values, old.Values) {
							t.Fatal("maintenance changed stable row/version/value provenance", old, row)
						}
					}
					if !reflect.DeepEqual(rows, before.Versions) || exec.PlannedVolatility(originalExecution) != expr.Immutable || originalDefinition.(plan.SQLRoutineExpr).Procedure != originalCall.Procedure {
						t.Fatal("maintenance mutated retained heap or original immutable recipe")
					}
					for address, root := range roots {
						old := catalog.MustRelation(address)
						current := e.CatalogRead().MustRelation(address)
						if old.SQLIndex.Parent != parent {
							if e.Storage.IndexRoots[address] != root || current != old {
								t.Fatal("maintenance changed an unrelated index")
							}
							continue
						}
						if (e.Storage.IndexRoots[address] != root) != (current.Name() == "v") || current.SQLIndex.Clustered != (current.Name() == selected) || current.SQLIndex.Ready != old.SQLIndex.Ready || current.SQLIndex.Valid != old.SQLIndex.Valid || current.SQLIndex.Live != old.SQLIndex.Live || current.SQLIndex.Method != old.SQLIndex.Method || current.SQLIndex.Unique != old.SQLIndex.Unique || !reflect.DeepEqual(current.SQLIndex.Include, old.SQLIndex.Include) {
							t.Fatal("wrong rebuild set or changed existing index flags/options", current.Name())
						}
						rebuilt := e.Storage.IndexRoots[address]
						members := len(after.Versions)
						if current.Name() == "v" && kind == "sibling_predicate" {
							members = 3
						}
						if rebuilt == nil || len(rebuilt.Entries) != members {
							t.Fatal("wrong sibling membership", current.Name(), rebuilt)
						}
						seenRows := make(map[ids.RowID]bool, members)
						for _, entry := range rebuilt.Entries {
							if entry.Position < 0 || entry.Position >= len(after.Versions) {
								t.Fatal("invalid sibling position", current.Name(), entry)
							}
							row := after.Versions[entry.Position]
							if seenRows[row.Row] || entry.Row != row.Row || entry.Version != row.Version {
								t.Fatal("lost sibling row/version authority", current.Name(), entry, row)
							}
							seenRows[row.Row] = true
						}
					}
					current := e.CatalogRead().MustRelation(ref.Address)
					var definition, execution expr.Expr
					if kind == "sibling_predicate" {
						definition, execution = current.SQLIndex.Predicate.Definition, current.SQLIndex.Predicate.Execution
					} else {
						definition, execution = current.SQLIndex.Keys[0].Derived.Definition, current.SQLIndex.Keys[0].Derived.Execution
					}
					wantVolatility := expr.Stable
					if volatility == "VOLATILE" {
						wantVolatility = expr.Volatile
					}
					for _, node := range []expr.Expr{definition, execution} {
						call := node.(plan.SQLRoutineExpr)
						if call.Ref != originalCall.Ref || call.Procedure != procedure || call.Info.RoutineReference != procedure || exec.PlannedVolatility(node) != wantVolatility {
							t.Fatal("refresh did not preserve exact identity and actual current volatility", call, wantVolatility)
						}
					}
					facts, err := index.ProjectIndexFacts(t.Context(), e.CatalogRead(), ref)
					if err != nil || facts.IndIsClustered != (selected == "v") || !facts.IndIsValid || !facts.IndIsReady || !facts.IndIsLive {
						t.Fatal("existing mutable recipe corrupted structural facts", facts, err)
					}
					wantKeys := map[int64]any{1: int64(12), 2: int64(11), 3: int64(12), 4: nil, 5: int64(13)}
					if kind == "sibling_predicate" {
						wantKeys = map[int64]any{1: int64(2), 3: int64(2), 5: int64(3)}
					}
					root := e.Storage.IndexRoots[ref.Address]
					if root == nil || len(root.Entries) != len(wantKeys) {
						t.Fatal("wrong actual expression/predicate root membership", root)
					}
					seen := make(map[int64]bool, len(wantKeys))
					for _, entry := range root.Entries {
						if entry.Position < 0 || entry.Position >= len(after.Versions) {
							t.Fatal("invalid actual physical root position", entry)
						}
						row := after.Versions[entry.Position]
						id := enginetest.Export(row.Values[0]).(int64)
						key, member := wantKeys[id]
						if !member || seen[id] || entry.Row != row.Row || entry.Version != row.Version || len(entry.Keys) != 1 || !reflect.DeepEqual(enginetest.Export(entry.Keys[0]), key) {
							t.Fatal("incorrect actual rebuilt key/membership/version authority", entry, row, key)
						}
						seen[id] = true
					}
				})
			}
		}
	}
}

func TestClusterChangedRoutineMembership(t *testing.T) {
	e, s, parent := clusterStorageSeed(t)
	for _, q := range []string{
		"CREATE FUNCTION public.cluster_membership(integer) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1>2$$",
		"CREATE INDEX membership_idx ON t(k) WHERE public.cluster_membership(k)",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	address := enginetest.CatalogRef(t, e, e.CatalogRead(), "membership_idx").Address
	old := e.CatalogRead().MustRelation(address)
	predicate := old.SQLIndex.Predicate
	call := predicate.Definition.(plan.SQLRoutineExpr)
	heap := e.Storage.Relations[parent]
	generation, ends, versions := heap.Generation, heap.Ends, heap.Versions
	roots := maps.Clone(e.Storage.IndexRoots)
	oldEntries := append([]storage.IndexEntry(nil), roots[address].Entries...)
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION public.cluster_membership(integer) RETURNS boolean LANGUAGE SQL STABLE AS $$SELECT 0; SELECT $1>1$$")
	enginetest.SessionSQL(t, s, "CLUSTER t USING a")
	if e.Storage.Relations[parent] != heap || heap.Generation != generation || heap.Ends != ends || !storage.SameVersionRoot(heap.Versions, versions) {
		t.Fatal("semantic rebuild replaced heap")
	}
	for a, root := range roots {
		if (e.Storage.IndexRoots[a] != root) != (a == address) {
			t.Fatal("semantic rebuild touched unrelated root", a)
		}
	}
	if old.SQLIndex.Predicate != predicate || predicate.Definition.(plan.SQLRoutineExpr).Procedure != call.Procedure || exec.PlannedVolatility(predicate.Execution) != expr.Immutable || !reflect.DeepEqual(roots[address].Entries, oldEntries) {
		t.Fatal("semantic rebuild mutated immutable predecessors")
	}
	if len(e.Storage.IndexRoots[address].Entries) != 3 {
		t.Fatal("semantic rebuild failed to expand partial membership")
	}
	enginetest.TransactionRows(t, s, "SELECT id FROM t WHERE public.cluster_membership(k) ORDER BY id", 1, 3, 5)
	for _, q := range []string{"INSERT INTO t VALUES(6,2,'new')", "UPDATE t SET k=1 WHERE id=3", "DELETE FROM t WHERE id=5"} {
		enginetest.SessionSQL(t, s, q)
	}
	enginetest.TransactionRows(t, s, "SELECT id FROM t WHERE public.cluster_membership(k) ORDER BY id", 1, 6)
}
