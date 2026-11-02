package functions_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeProviderTargetValidation(t *testing.T) {
	for _, kind := range []string{"partitioned_index", "nonheap_method"} {
		t.Run(kind, func(t *testing.T) {
			e, heap, node, populated := surgeryPublicationFixture(t)
			for _, q := range []string{
				"CREATE ROLE surgery_reader",
				"GRANT EXECUTE ON FUNCTION heap_force_freeze(regclass,tid[]) TO surgery_reader",
			} {
				enginetest.Run(t, e, q)
			}
			target := heap
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR"}
			if kind == "partitioned_index" {
				// Use a real published partitioned index. UNIQUE creation is already
				// supported and does not need a new index construction mechanism.
				enginetest.Run(t, e, "CREATE TABLE surgery_parent(n integer) PARTITION BY RANGE(n)")
				enginetest.Run(t, e, "CREATE UNIQUE INDEX surgery_parent_index ON surgery_parent(n)")
				view := e.CatalogRead()
				target = enginetest.CatalogRef(t, e, view, "surgery_parent_index")
				index := view.MustRelation(target.Address)
				if index.SQLIndex == nil || view.MustRelation(index.SQLIndex.Parent).Partition == nil {
					t.Fatal("missing genuine partitioned-index descriptor")
				}
				want.Code = "42809"
				want.Message = `cannot operate on relation "surgery_parent_index"`
				want.Detail = "This operation is not supported for partitioned indexes."
			} else {
				// Native table-AM construction is not part of surgery. Publish an
				// owned descriptor using the genuine heap authority under a distinct
				// method identity, then invoke the actual installed runtime. Released
				// heap_force_common checks relam, not the handler's implementation.
				view := e.CatalogRead()
				builtin, ok := view.AccessMethod(2)
				if !ok {
					t.Fatal("missing genuine heap method")
				}
				method := *builtin
				method.ObjectAddress = ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: enginetest.CatalogUnitOID(t, e)}
				method.ObjectName = "surgery_heap_alias"
				method.ObjectRevision = 1
				original := view.MustRelation(heap.Address)
				next := *original
				next.TableMethod = method.Address().ObjectID
				next.ObjectRevision = next.Revision() + 1
				before := e.Catalog.State
				t.Cleanup(func() {
					e.Mu.Lock()
					e.Catalog.PublishLocked(before)
					e.Mu.Unlock()
				})
				state := catalog.CloneState(before)
				state.SetDescriptor(method.Address(), &method)
				state.BindName(catalog.DescriptorNameKey(&method), method.Address())
				state.SetDescriptor(heap.Address, &next)
				e.Mu.Lock()
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
				if original.TableMethod != 2 || next.TableMethod == 2 || method.Native != builtin.Native || method.Handler != builtin.Handler {
					t.Fatal("method fixture changed original descriptor or native authority")
				}
				want.Code = "0A000"
				want.Message = "only heap AM is supported"
			}
			array, _ := e.CatalogRead().LookupType(sqltype.ID(1010))
			empty, err := sqltype.ArrayValue(array, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			store := e.Storage.Relations[heap.Address]
			generation, headers := store.Generation, store.Headers
			versions := store.Versions
			for _, role := range []string{"postgres", "surgery_reader"} {
				t.Run(role, func(t *testing.T) {
					observer := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, observer, "SET SESSION AUTHORIZATION "+role)
					for _, input := range []sqltype.Value{empty, populated} {
						scope, finish := visibilityScope(t, e, observer)
						err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(target.Address.ObjectID), input)
						err = finish(err)
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
							t.Fatalf("target admission with %d TIDs = %#v (%v), want %#v", input.ArrayLen(), diagnostic, err, want)
						}
						if store.Generation != generation || store.Headers != headers || len(store.Versions) != len(versions) || &store.Versions[0] != &versions[0] {
							t.Fatal("rejected target admission published heap state")
						}
					}
				})
			}
		})
	}
}
