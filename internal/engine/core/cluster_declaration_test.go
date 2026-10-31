package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/sqlstate"
)

// A virtual parent's old routine carrier cannot be refreshed by public CLUSTER
// (it has no heap). Supply a coherent current descriptor privately, without
// weakening the normal stale-program checks, to exercise the constructor seam.
func TestClusterPartitionDeclarationAdmission(t *testing.T) {
	for _, volatility := range []string{"STABLE", "VOLATILE"} {
		for _, predicate := range []bool{false, true} {
			for _, reuse := range []bool{false, true} {
				name := volatility + map[bool]string{false: "/expression", true: "/predicate"}[predicate] + map[bool]string{false: "/synthesis", true: "/reuse"}[reuse]
				t.Run(name, func(t *testing.T) {
					e, s, _ := clusterStorageSeed(t)
					result, body, key := "integer", "SELECT 0; SELECT $1+10", "(id,public.declaration_key(k))"
					message := "functions in index expression must be marked IMMUTABLE"
					if predicate {
						result, body, key = "boolean", "SELECT 0; SELECT $1>1", "(id) WHERE public.declaration_key(k)"
						message = "functions in index predicate must be marked IMMUTABLE"
					}
					for _, q := range []string{
						"CREATE FUNCTION public.declaration_key(integer) RETURNS " + result + " LANGUAGE SQL IMMUTABLE AS $$" + body + "$$",
						"CREATE INDEX declaration_current ON t" + key,
						"CREATE TABLE declaration_root(LIKE t) PARTITION BY RANGE(id)",
						"CREATE TABLE declaration_leaf PARTITION OF declaration_root FOR VALUES FROM(0) TO(10)",
						"CREATE INDEX declaration_leaf_plain ON declaration_leaf(k)",
					} {
						enginetest.SessionSQL(t, s, q)
					}
					parentKey := "(id,k)"
					if predicate {
						parentKey = "(id)"
					}
					enginetest.SessionSQL(t, s, "CREATE UNIQUE INDEX declaration_parent ON ONLY declaration_root"+parentKey)
					if reuse {
						enginetest.SessionSQL(t, s, "CREATE INDEX declaration_reusable ON declaration_leaf"+key)
					}
					enginetest.SessionSQL(t, s, "ALTER FUNCTION public.declaration_key(integer) "+volatility)
					enginetest.SessionSQL(t, s, "CLUSTER t USING a")
					enginetest.SessionSQL(t, s, "CLUSTER declaration_leaf USING declaration_leaf_plain")
					var history []*catalog.Relation
					var expected []expr.Volatility
					for _, transition := range []string{volatility, "IMMUTABLE", volatility} {
						if len(history) != 0 {
							enginetest.SessionSQL(t, s, "ALTER FUNCTION public.declaration_key(integer) "+transition)
							enginetest.SessionSQL(t, s, "CLUSTER t USING a")
						}
						read := e.CatalogRead()
						ref := enginetest.CatalogRef(t, e, read, "declaration_current")
						current := read.MustRelation(ref.Address)
						history = append(history, current)
						want := expr.Immutable
						if transition == "STABLE" {
							want = expr.Stable
						} else if transition == "VOLATILE" {
							want = expr.Volatile
						}
						expected = append(expected, want)
						for i, retained := range history {
							var definition, execution expr.Expr
							if predicate {
								definition, execution = retained.SQLIndex.Predicate.Definition, retained.SQLIndex.Predicate.Execution
							} else {
								definition, execution = retained.SQLIndex.Keys[1].Derived.Definition, retained.SQLIndex.Keys[1].Derived.Execution
							}
							for _, node := range []expr.Expr{definition, execution} {
								call := node.(plan.SQLRoutineExpr)
								if err := call.ValidateStoredProvider(); err != nil || call.Property != call.GroupProperties(call.Args) || call.Property.Volatility != expected[i] || call.Procedure.Volatility != expected[i] {
									t.Fatal("repeated refresh corrupted current or retained call properties", err)
								}
								if retained == current {
									procedure, ok := read.Procedure(call.Ref.Address)
									if !ok || call.Procedure != procedure || call.Info.RoutineReference != procedure || call.Property.Volatility != want {
										t.Fatal("refresh retained historical declaration volatility", transition, call)
									}
								}
							}
						}
						if len(history) > 1 && history[len(history)-2] == current {
							t.Fatal("attribute transition reused its predecessor descriptor")
						}
					}
					view := e.CatalogRead()
					get := func(name string) *catalog.Relation {
						return view.MustRelation(enginetest.CatalogRef(t, e, view, name).Address)
					}
					source, current, root, parent, leaf := get("t"), get("declaration_current"), get("declaration_root"), get("declaration_parent"), get("declaration_leaf")
					// Map the genuine refreshed routine program onto the virtual
					// parent; retain its original identity, flags and providers.
					mapped, err := index.MapPartitionIndex(t.Context(), view, source, root, current.SQLIndex)
					if err != nil {
						t.Fatal(err)
					}
					mapped.Valid, mapped.Ready, mapped.Live = parent.SQLIndex.Valid, parent.SQLIndex.Ready, parent.SQLIndex.Live
					mapped.Clustered = false
					coherent := *parent
					coherent.SQLIndex = mapped
					coherent.Columns = slices.Clone(current.Columns)
					coherent.Attributes = slices.Clone(current.Attributes)
					coherent.ColumnMeta = current.ColumnMeta
					coherent.Index = maps.Clone(current.Index)
					for i := range coherent.Columns {
						coherent.Columns[i].OriginTable = uint32(parent.Address().ObjectID)
					}
					next := catalog.CloneState(view.State())
					next.SetDescriptor(parent.Address(), &coherent)
					e.Mu.Lock()
					e.Catalog.PublishLocked(next)
					e.Mu.Unlock()
					view = e.CatalogRead()
					if err := index.ValidateIndexDefinition(view, &coherent); err != nil {
						t.Fatal("coherent current program failed strict validation", err)
					}
					beforeRoots := maps.Clone(e.Storage.IndexRoots)
					op, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					stage := e.BeginCatalogStage(view)
					defer e.DiscardCatalogStage(stage)
					auth, err := core.OperationAuthorization(op)
					if err != nil {
						t.Fatal(err)
					}
					e.Mu.Lock()
					child, err := index.StagePartitionIndexChild(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, root, &coherent, leaf, true)
					e.Mu.Unlock()
					if reuse {
						original := get("declaration_reusable")
						if err != nil || child == nil || child.Address() != original.Address() || child.SQLIndex.PartitionParent != parent.Address() {
							t.Fatal("compatible existing child was subjected to CREATE admission", child, err)
						}
					} else {
						var diagnostic *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: message}
						if child != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
							t.Fatalf("synthesis = %v, %#v (%v), want %#v", child, diagnostic, err, want)
						}
						if !maps.Equal(maps.Collect(e.StageRead(stage).Descriptors()), maps.Collect(view.Descriptors())) {
							t.Fatal("failed synthesis leaked staged descriptors")
						}
					}
					if e.Catalog.State != next || !maps.Equal(beforeRoots, e.Storage.IndexRoots) || parent.SQLIndex == mapped {
						t.Fatal("constructor changed live publication or historical recipe")
					}
				})
			}
		}
	}
}
