package core_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Supplement TestXML2ConfigurationPublication with actual composed index owners.
// Public exact results and PostgreSQL expectations stay in SetsControls.
func xml2MixedIndexPublication(t *testing.T) {
	const nodes = "xpath_nodeset(doc,path)"
	const list = "xpath_list(doc,path)"
	for _, shape := range []struct {
		name, expression string
		predicate bool
		occurrences, initialOccurrences int
	}{
		{"key", nodes + " || " + list, false, 2, 2},
		{"predicate", nodes + " <> '' AND " + list + " <> ''", true, 2, 2},
		{"nested_nodes", "xpath_nodeset('<r><a>' || " + list + " || '</a></r>','/r/a')", false, 2, 2},
		{"nested_list", "xpath_list('<r>' || " + nodes + " || '</r>','/r/a')", false, 2, 2},
		{"repeated", nodes + " || " + list + " || " + nodes, false, 3, 3},
		{"folded_selected", "xpath_nodeset('<r><a>A</a></r>','/r/a') || " + list, false, 2, 1},
	} {
		t.Run(shape.name, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION xml2")
			enginetest.Run(t, e, "CREATE ROLE xml2_mixed_kernel_owner")
			enginetest.Run(t, e, "ALTER FUNCTION xpath_nodeset(text,text,text,text) OWNER TO xml2_mixed_kernel_owner")
			enginetest.Run(t, e, "ALTER FUNCTION xpath_list(text,text,text) OWNER TO xml2_mixed_kernel_owner")
			enginetest.Run(t, e, "CREATE TABLE xml2_mixed_owned(id integer,doc text,path text)")
			enginetest.Run(t, e, "INSERT INTO xml2_mixed_owned VALUES(1,'<r><a>A</a></r>','/r/a')")
			create := "CREATE INDEX xml2_mixed_owned_index ON xml2_mixed_owned ((" + shape.expression + "))"
			if shape.predicate {
				create = "CREATE INDEX xml2_mixed_owned_index ON xml2_mixed_owned (id) WHERE " + shape.expression
			}
			enginetest.Run(t, e, create)
			// A later genuine owner revision must not replace either lane's
			// original bind-time kernel with the current descriptor.
			enginetest.Run(t, e, "CREATE ROLE xml2_mixed_current_kernel_owner")
			enginetest.Run(t, e, "ALTER FUNCTION xpath_nodeset(text,text,text,text) OWNER TO xml2_mixed_current_kernel_owner")
			enginetest.Run(t, e, "ALTER FUNCTION xpath_list(text,text,text) OWNER TO xml2_mixed_current_kernel_owner")
			initial := e.CatalogRead()
			header := initial.MustExtension(initial.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
			relation := func(read catalog.Read, name string) *catalog.Relation {
				return read.MustRelation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}))
			}
			derived := func(index *catalog.Relation) catalog.DerivedIndexKey {
				if predicate := index.SQLIndex.Predicate; shape.predicate {
					return catalog.DerivedIndexKey{Definition: predicate.Definition, Execution: predicate.Execution, Dependencies: predicate.Dependencies, Attributes: predicate.Attributes}
				}
				return *index.SQLIndex.Keys[0].Derived
			}
			calls := func(node expr.Expr) []plan.InstalledXML2Expr {
				var result []plan.InstalledXML2Expr
				plan.WalkExpression(node, func(node expr.Expr) {
					if call, ok := node.(plan.InstalledXML2Expr); ok {
						result = append(result, call)
					}
				})
				return result
			}
			initialCalls := calls(derived(relation(initial, "xml2_mixed_owned_index")).Execution)
			if len(initialCalls) != shape.initialOccurrences {
				t.Fatal("fixture lost its independently expected execution occurrences", initialCalls)
			}
			for _, call := range initialCalls {
				if !call.Inline || call.Kernel == nil || call.Procedure.Configuration != nil {
					t.Fatal("fixture did not cook genuine unconfigured inline execution", call)
				}
				if call.Kernel.Revision() == header.Installation.Declarations[call.Kernel.Origin.Declaration].Revision() {
					t.Fatal("mixed fixture retained a canonical kernel revision")
				}
				if call.Kernel.Revision() == initial.MustProcedure(call.Kernel.Address()).Revision() {
					t.Fatal("mixed fixture did not retain a historical bind-time kernel")
				}
			}
			foreign := engine.New().CatalogRead()
			configuredNegative := false
			for _, change := range []struct {
				slot int
				clause string
			}{
				{7, "SET client_min_messages = warning"},
				{7, "RESET client_min_messages"},
				{9, "SET client_min_messages = warning"},
				{9, "RESET client_min_messages"},
				{7, "SET client_min_messages = warning"},
				{9, "SET client_min_messages = warning"},
				{7, "RESET client_min_messages"},
				{9, "RESET client_min_messages"},
			} {
				source := e.CatalogRead()
				before := source.MustProcedure(header.Members[change.slot])
				otherSlot := 7
				if change.slot == 7 {
					otherSlot = 9
				}
				unrelated := source.MustProcedure(header.Members[otherSlot])
				oldIndex := relation(source, "xml2_mixed_owned_index")
				oldTable := relation(source, "xml2_mixed_owned")
				oldDerived := derived(oldIndex)
				originalIndex := oldIndex.SQLIndex
				dependencies := slices.Clone(oldDerived.Dependencies)
				originalCalls := calls(oldDerived.Execution)
				originalDefinitions := calls(oldDerived.Definition)
				savedCalls := slices.Clone(originalCalls)
				for i := range savedCalls {
					savedCalls[i].Args = slices.Clone(savedCalls[i].Args)
				}
				root, rows := e.Storage.IndexRoots[oldIndex.Address()], e.Storage.Relations[oldTable.Address()]
				if root == nil || len(root.Entries) == 0 {
					t.Fatal("fixture lacks its populated physical root", root)
				}
				entries := slices.Clone(root.Entries)
				entryBacking := &root.Entries[0]
				assertOriginal := func() {
					t.Helper()
					if source.Get(oldIndex.Address()) != oldIndex || source.Get(before.Address()) != before || source.Get(unrelated.Address()) != unrelated ||
						oldIndex.SQLIndex != originalIndex || !reflect.DeepEqual(derived(oldIndex), oldDerived) || !slices.Equal(oldDerived.Dependencies, dependencies) ||
						!reflect.DeepEqual(root.Entries, entries) || &root.Entries[0] != entryBacking {
						t.Fatal("publication mutated original owner, captures, graph or physical backing")
					}
					currentCalls := calls(oldDerived.Execution)
					if len(currentCalls) != len(savedCalls) {
						t.Fatal("original execution occurrence inventory changed")
					}
					for i, call := range currentCalls {
						if !reflect.DeepEqual(call, savedCalls[i]) || &call.Args[0] != &originalCalls[i].Args[0] {
							t.Fatal("original execution carrier or operand backing changed", i)
						}
					}
				}
				enginetest.Run(t, e, "ALTER FUNCTION "+before.Name()+"(text,text) "+change.clause)
				target := e.CatalogRead()
				after := target.MustProcedure(before.Address())
				currentIndex := target.MustRelation(oldIndex.Address())
				currentDerived := derived(currentIndex)
				if after == before || after.Revision() <= before.Revision() || target.MustProcedure(unrelated.Address()) != unrelated ||
					currentIndex == oldIndex || !stored.ScalarRelationSuccessor(target, oldIndex, currentIndex) ||
					e.Storage.IndexRoots[oldIndex.Address()] != root || e.Storage.Relations[oldTable.Address()] != rows {
					t.Fatal("mixed publication lost checked successor, unrelated identity or physical root")
				}
				if err := target.Owner().Provider(ids.RelationClassID).Validate(target, currentIndex); err != nil {
					t.Fatal("mixed index successor invalid", err)
				}
				if !slices.Equal(currentDerived.Dependencies, dependencies) || !reflect.DeepEqual(currentDerived.Definition, currentDerived.Execution) {
					t.Fatal("mixed execution was not reconstructed from the strict definition with original captures")
				}
				for _, node := range []expr.Expr{currentDerived.Definition, currentDerived.Execution} {
					published := calls(node)
					if len(published) != shape.occurrences {
						t.Fatal("mixed publication lost an actual occurrence")
					}
					for _, call := range published {
						var original *plan.InstalledXML2Expr
						for i := range originalDefinitions {
							candidate := &originalDefinitions[i]
							if candidate.Ref == call.Ref && candidate.Source == call.Source && candidate.Occurrence == call.Occurrence {
								original = candidate
								break
							}
						}
						if original == nil {
							t.Fatal("published occurrence lost its original Definition")
						}
						expected := original.Procedure
						if call.Ref.Address == before.Address() {
							expected = after
						}
						if call.Kernel == nil {
							t.Fatal("mixed publication discarded an original kernel")
						}
						originalKernel := original.Kernel
						if call.Inline || call.Procedure != expected || call.RoutineReference != expected || call.Kernel != originalKernel ||
							call.Ref.Domain() != source.Domain() || call.Ref.Epoch != source.Epoch() {
							t.Fatal("mixed publication lost strict selected/unrelated/kernel identity", call)
						}
						if err := stored.ValidateInstalledXML2(call, target); err != nil {
							t.Fatal("mixed publication lost exact properties or original lifetime", err)
						}
					}
				}
				assertOriginal()

				// Corrupt only the unrelated occurrence or its actual capture in
				// this original owner. Definition and execution stay separate lanes.
				kinds := []string{"missing-kernel", "stale-kernel", "stale-outer", "foreign-ref", "wrong-body", "wrong-source", "wrong-occurrence", "missing-outer-capture", "missing-inner-capture", "inline-definition"}
				var unrelatedCalls []plan.InstalledXML2Expr
				for _, call := range originalCalls {
					if call.Ref.Address == unrelated.Address() {
						unrelatedCalls = append(unrelatedCalls, call)
					}
				}
				if len(unrelatedCalls) > 1 {
					kinds = append(kinds, "duplicate-occurrence")
				}
				if unrelated.Configuration != nil {
					kinds = append(kinds, "configured-inline")
					configuredNegative = true
				}
				for _, kind := range kinds {
					bad := *oldIndex
					index := *oldIndex.SQLIndex
					index.Keys = slices.Clone(index.Keys)
					copy := oldDerived
					copy.Dependencies = slices.Clone(dependencies)
					if kind == "missing-outer-capture" || kind == "missing-inner-capture" {
						address := unrelated.Address()
						if kind == "missing-inner-capture" {
							for _, call := range originalCalls {
								if call.Ref.Address == unrelated.Address() {
									address = call.Kernel.Address()
								}
							}
						}
						copy.Dependencies = slices.DeleteFunc(copy.Dependencies, func(ref ids.ObjectRef) bool { return ref.Address == address })
						if len(copy.Dependencies) != len(dependencies)-1 {
							t.Fatal("fixture did not remove one actual unrelated capture", kind)
						}
					} else {
						mutated := 0
						var rewrite func(expr.Expr) (expr.Expr, bool, error)
						rewrite = func(node expr.Expr) (expr.Expr, bool, error) {
							if call, ok := node.(plan.InstalledXML2Expr); ok {
								call.Args = slices.Clone(call.Args)
								for i, arg := range call.Args {
									next, _, err := rewrite(arg)
									if err != nil {
										return nil, false, err
									}
									call.Args[i] = next
								}
								if call.Ref.Address == unrelated.Address() && mutated == 0 {
									mutated++
									switch kind {
									case "missing-kernel":
										call.Kernel = nil
									case "stale-kernel":
										kernel := *call.Kernel
										kernel.ObjectRevision++
										call.Kernel = &kernel
									case "stale-outer":
										procedure := *call.Procedure
										procedure.ObjectRevision++
										call.Procedure, call.RoutineReference = &procedure, &procedure
									case "wrong-source":
										call.Source.Input.Offset++
									case "wrong-occurrence":
										call.Occurrence++
									case "duplicate-occurrence":
										call.Source = unrelatedCalls[1].Source
										call.Occurrence = unrelatedCalls[1].Occurrence
									case "foreign-ref":
										call.Ref = foreign.Domain().Ref(call.Ref.Epoch, call.Ref.Address)
									case "wrong-body":
										procedure := *call.Procedure
										procedure.DeclarationBody = nil
										call.Procedure, call.RoutineReference = &procedure, &procedure
									case "inline-definition", "configured-inline":
										call.Inline = true
									}
								}
								return call, true, nil
							}
							return stored.MapExpression(source, node, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
								if _, ok := child.(plan.InstalledXML2Expr); ok {
									return rewrite(child)
								}
								return child, false, nil
							})
						}
						node := copy.Execution
						if kind == "inline-definition" {
							node = copy.Definition
						}
						next, _, err := rewrite(node)
						if err != nil || mutated != 1 {
							t.Fatal("construct unrelated source corruption", kind, mutated, err)
						}
						if kind == "inline-definition" {
							copy.Definition = next
						} else {
							copy.Execution = next
						}
					}
					if shape.predicate {
						predicate := catalog.IndexPredicate(copy)
						index.Predicate = &predicate
					} else {
						index.Keys[0].Derived = &copy
					}
					bad.SQLIndex = &index
					state := catalog.CloneState(source.State())
					state.SetDescriptor(bad.Address(), &bad)
					badSource := source.WithState(state)
					destination := catalog.CloneState(state)
					destination.SetDescriptor(after.Address(), after)
					e.Mu.Lock()
					publication, err := stored.RefreshStoredScalarRoutine(badSource, source.WithState(destination), before, after, e.Catalog.RevisionLocked)
					e.Mu.Unlock()
					if err == nil || publication != nil || e.CatalogRead().State() != target.State() || e.Storage.IndexRoots[oldIndex.Address()] != root ||
						e.Storage.Relations[oldTable.Address()] != rows {
						t.Fatal("forged unrelated index occurrence adopted publication authority", kind, err)
					}
					assertOriginal()
				}
				for _, query := range []string{
					"INSERT INTO xml2_mixed_owned VALUES(2,'<r><a>B</a></r>','/r/a')",
					"UPDATE xml2_mixed_owned SET doc='<r><a>C</a></r>' WHERE id=2",
					"SELECT " + shape.expression + " FROM xml2_mixed_owned ORDER BY id",
					"DELETE FROM xml2_mixed_owned WHERE id=2",
				} {
					enginetest.Run(t, e, query)
				}
			}
			if !configuredNegative {
				t.Fatal("fixture never rejected a genuine configured unrelated inline carrier")
			}
		})
	}
}
