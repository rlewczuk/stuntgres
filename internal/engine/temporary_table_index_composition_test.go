package engine

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTemporaryTableIndexComposition(t *testing.T) {
	for _, mode := range []string{"Ordinary", "OrdinaryOverflow", "TerminalMaximum"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			for _, q := range []string{
				"CREATE TEMP TABLE composition_owner(id SERIAL)",
				"CREATE TEMP TABLE composition_survivor(a INT,g BOOL GENERATED ALWAYS AS('composition_owner'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS('composition_owner_id_seq'::regclass IS NOT NULL) STORED,z INT)",
				"CREATE INDEX composition_index ON composition_survivor((z+0))",
				"INSERT INTO composition_survivor(a,z) VALUES(7,9)",
			} {
				sessionSQL(t, owner, q)
			}
			var provider, survivor, index *relationDescriptor
			for _, descriptor := range e.state.descriptors {
				if d, ok := descriptor.(*relationDescriptor); ok {
					switch d.name {
					case "composition_owner":
						provider = d
					case "composition_survivor":
						survivor = d
					case "composition_index":
						index = d
					}
				}
			}
			if provider == nil || survivor == nil || index == nil {
				t.Fatal("missing genuine composition fixture")
			}
			originalCatalog := e.state
			originalIndex := index
			if mode != "Ordinary" {
				maximum := *index
				maximum.revision = ids.DefinitionRevision(^uint64(0))
				if mode == "OrdinaryOverflow" {
					maximum.revision--
				}
				index = &maximum
				e.state = cloneCatalog(e.state)
				e.state.descriptors[index.address] = index
				t.Cleanup(func() { e.state = originalCatalog })
			}
			before := e.storage[survivor.address]
			oldRows := before.versions
			oldRoot := e.indexRoots[index.address]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			stage.terminalCleanup = mode == "TerminalMaximum"
			changes := cloneCatalogChanges(nil)
			project := func(old, next *relationDescriptor) error {
				_, _, _, err := stagePrivateColumnShape(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: stage.base}, stage, changes, old, next, nil, nil, transactionRoot{}, false, false)
				return err
			}
			providerRef := e.domain.Ref(stage.base.Epoch(), provider.address)
			if err := stage.Remove(providerRef); err != nil {
				t.Fatal(err)
			}
			if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, changes, project, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			intermediate := stage.descriptors[index.address].(*relationDescriptor)
			intermediateParent := stage.descriptors[survivor.address].(*relationDescriptor)
			check := func(candidate, parent *relationDescriptor, wantSlot int, wantLayout []int32) {
				t.Helper()
				derived := candidate.sqlIndex.keys[0].derived
				if !slices.Equal(derived.attributes, wantLayout) {
					t.Fatal("captured layout", derived.attributes, wantLayout)
				}
				for _, expression := range []BoundExpr{derived.definition, derived.execution} {
					node := expression.(arithmeticExpression).left
					for {
						cast, ok := node.(coercionExpression)
						if !ok {
							break
						}
						node = cast.child
					}
					column := node.(columnExpression)
					if column.slot != wantSlot || column.origin.Address.SubID != 4 {
						t.Fatal("derived slot/attribute", column.slot, column.origin.Address)
					}
				}
				if candidate.name != index.name || candidate.owner != index.owner || candidate.namespace != index.namespace || candidate.sqlIndex.ready != index.sqlIndex.ready || candidate.sqlIndex.valid != index.sqlIndex.valid || candidate.sqlIndex.live != index.sqlIndex.live {
					t.Fatal("index identity or flags changed")
				}
				if err := validateIndexDefinition(e.stageRead(stage), candidate); err != nil {
					t.Fatal(err)
				}
				if len(parent.columns) != len(wantLayout) {
					t.Fatal("parent shape")
				}
			}
			check(intermediate, intermediateParent, 2, []int32{1, 3, 4})
			if intermediate == index || intermediate.sqlIndex.keys[0].derived == index.sqlIndex.keys[0].derived {
				t.Fatal("first candidate mutated original")
			}
			err := stageOwnedExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, changes, project)
			if mode == "OrdinaryOverflow" {
				if err == nil || err.Error() != "stuntgres: index revision exhausted" {
					t.Fatal("ordinary second evolution overflow", err)
				}
				if e.indexRoots[index.address] != oldRoot || e.storage[survivor.address] != before {
					t.Fatal("failed evolution changed live roots")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			final := stage.descriptors[index.address].(*relationDescriptor)
			finalParent := stage.descriptors[survivor.address].(*relationDescriptor)
			check(final, finalParent, 1, []int32{1, 4})
			if final == intermediate || final.sqlIndex.keys[0].derived == intermediate.sqlIndex.keys[0].derived {
				t.Fatal("second candidate mutated first")
			}
			if !slices.Equal(index.sqlIndex.keys[0].derived.attributes, []int32{1, 2, 3, 4}) || !slices.Equal(intermediate.sqlIndex.keys[0].derived.attributes, []int32{1, 3, 4}) || len(oldRows[0].values) != 4 || e.indexRoots[index.address] != oldRoot {
				t.Fatal("retained candidate/vector/root mutated")
			}
			for _, retained := range []struct {
				candidate *relationDescriptor
				slot      int
			}{{index, 3}, {intermediate, 2}} {
				derived := retained.candidate.sqlIndex.keys[0].derived
				for _, expression := range []BoundExpr{derived.definition, derived.execution} {
					node := expression.(arithmeticExpression).left
					for {
						cast, ok := node.(coercionExpression)
						if !ok {
							break
						}
						node = cast.child
					}
					if column := node.(columnExpression); column.slot != retained.slot || column.origin.Address.SubID != 4 {
						t.Fatal("retained derived tree mutated", column.slot, retained.slot)
					}
				}
			}
			if mode == "TerminalMaximum" && (intermediate.revision != index.revision || final.revision != index.revision) || mode == "Ordinary" && final.revision != index.revision+2 {
				t.Fatal("composition revision frontier")
			}
			e.mu.Lock()
			err = e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
			if err == nil {
				e.stageCatalogChangesLocked(stage, changes)
			}
			e.mu.Unlock()
			if err == nil {
				op, acquireErr := owner.Acquire(context.Background())
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				scope, scopeErr := e.StartStatement(op)
				if scopeErr != nil {
					op.Done()
					t.Fatal(scopeErr)
				}
				err = scope.gate(statement(t, "SELECT 1"))
				if err == nil {
					err = e.prepareCatalogOverlayIndexes(scope, stage, changes)
				}
				_ = scope.Finish(context.Canceled)
				op.Done()
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage.descriptors[index.address] != final {
				t.Fatal("coherent validation evolved again")
			}
			if changes.originals[index.address] != index || changes.storage[survivor.address].original != before || !sameVersionRoot(changes.storage[survivor.address].root, oldRows) || changes.storage[survivor.address].ends != before.ends || changes.storage[survivor.address].generation != before.generation {
				t.Fatal("original publication predecessor lost")
			}
			root := changes.uniqueRoots[index.address]
			if root == nil || root == oldRoot || len(root.entries) != 1 {
				t.Fatal("missing actual rebuilt index root")
			}
			key, kerr := root.entries[0].keys[0].Integer()
			if kerr != nil || key != 9 || root.entries[0].row != oldRows[0].row || root.entries[0].version != oldRows[0].version {
				t.Fatal("rebuilt key/identity", key, kerr)
			}
			if originalIndex.sqlIndex.keys[0].derived.attributes[3] != 4 || before != e.storage[survivor.address] {
				t.Fatal("original fixture changed")
			}
		})
	}
}

func TestTemporaryTableIndexDefinitionOwnNamespace(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, session := range []*Session{owner, peer} {
		sessionSQL(t, session, "CREATE TEMP TABLE deparse_owner_table(v INT)")
		sessionSQL(t, session, "CREATE INDEX deparse_owner_index ON deparse_owner_table(v)")
	}
	view := e.CatalogRead()
	var own, foreign *relationDescriptor
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.name != "deparse_owner_index" || index.sqlIndex == nil {
			continue
		}
		namespace := view.state.descriptors[index.namespace].(*namespaceDescriptor)
		if namespace.temporary == owner.token {
			own = index
		}
		if namespace.temporary == peer.token {
			foreign = index
		}
	}
	if own == nil || foreign == nil {
		t.Fatal("missing actual physical-owner indexes")
	}
	presentation := indexPresentation{temporary: own.namespace}
	actual, err := indexDefinitionSQL(context.Background(), view, own, presentation)
	if err != nil || actual != "CREATE INDEX deparse_owner_index ON pg_temp.deparse_owner_table USING btree (v)" {
		t.Fatal("own namespace alias", actual, err)
	}
	foreignNamespace := view.state.descriptors[foreign.namespace].Name()
	actual, err = indexDefinitionSQL(context.Background(), view, foreign, presentation)
	if err != nil || actual != "CREATE INDEX deparse_owner_index ON "+foreignNamespace+".deparse_owner_table USING btree (v)" {
		t.Fatal("foreign owner acquired own namespace alias", actual, err)
	}
	presentation.dump = true
	actual, err = indexDefinitionSQL(context.Background(), view, own, presentation)
	physicalNamespace := view.state.descriptors[own.namespace].Name()
	expectedPrefix := "CREATE INDEX " + quoteIdentifier("deparse_owner_index") + " ON " + quoteIdentifier(physicalNamespace) + "." + quoteIdentifier("deparse_owner_table") + " USING "
	if err != nil || !strings.HasPrefix(actual, expectedPrefix) {
		t.Fatal("context-free dump acquired alias", actual, expectedPrefix, err)
	}
}

func TestTemporaryTablePredicateOnlyRetainedTypes(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE retained_partial(v INT)")
	sessionSQL(t, owner, "CREATE INDEX retained_partial_index ON retained_partial(v) WHERE v>0")
	sessionSQL(t, owner, "INSERT INTO retained_partial VALUES(7),(-3)")
	before := e.CatalogRead()
	var parent, index *relationDescriptor
	for _, descriptor := range before.state.descriptors {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		switch relation.name {
		case "retained_partial":
			parent = relation
		case "retained_partial_index":
			index = relation
		}
	}
	if parent == nil || index == nil || index.sqlIndex.predicate == nil || index.sqlIndex.keys[0].derived != nil || len(parent.generations) != 0 || len(parent.defaults) != 0 || len(parent.checkConstraints) != 0 || len(parent.columns) != 1 || parent.columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("fixture is not a genuine unmasked plain-key partial predicate")
	}
	predicate := index.sqlIndex.predicate
	definitionTypes, executionTypes := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, tree := range []struct {
		node  BoundExpr
		types map[sqltype.ID]*sqltype.Descriptor
	}{
		{predicate.definition, definitionTypes}, {predicate.execution, executionTypes},
	} {
		walkBoundExpression(tree.node, func(node BoundExpr) { typ := node.ResultUsage().Type; tree.types[typ.ID()] = typ })
		if tree.types[sqltype.Bool] == nil || tree.types[sqltype.Int4] == nil {
			t.Fatal("missing actual predicate BOOL/INT4", tree.types)
		}
	}
	definitionBefore, err := (boundTree{expression: predicate.definition}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executionBefore, err := (boundTree{expression: predicate.execution}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := slices.Clone(predicate.dependencies)
	attributes := slices.Clone(predicate.attributes)
	store, root := e.storage[parent.address], e.indexRoots[index.address]
	if store == nil || len(store.versions) != 2 || root == nil || len(root.entries) != 1 {
		t.Fatal("missing actual rows or partial membership root")
	}
	rows, ends, generation := store.versions, store.ends, store.generation
	// Real foreign-engine replacement manufactures fresh builtin generations;
	// exact TEMP trees/rows/roots must retain theirs, not be rebound to new types.
	if _, err := e.ReplaceCatalog(context.Background(), New().CatalogRead()); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	if after.state == before.state || after.Epoch() <= before.Epoch() || after.state.descriptors[parent.address] != parent || after.state.descriptors[index.address] != index || index.sqlIndex.predicate != predicate || e.storage[parent.address] != store || e.indexRoots[index.address] != root || !sameVersionRoot(store.versions, rows) || store.ends != ends || store.generation != generation {
		t.Fatal("replacement changed actual retained catalog/index/store/vector roots")
	}
	for _, tree := range []struct {
		name  string
		node  BoundExpr
		types map[sqltype.ID]*sqltype.Descriptor
	}{
		{"definition", predicate.definition, definitionTypes}, {"execution", predicate.execution, executionTypes},
	} {
		for id, typ := range tree.types {
			actual, ok := after.LookupType(id)
			if !ok || actual != typ {
				t.Fatal("predicate concrete type generation not retained", tree.name, id, actual, typ)
			}
		}
		walkBoundExpression(tree.node, func(node BoundExpr) {
			typ := node.ResultUsage().Type
			if tree.types[typ.ID()] != typ {
				t.Fatal("immutable predicate type rebound", tree.name, typ)
			}
		})
	}
	definitionAfter, err := (boundTree{expression: predicate.definition}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executionAfter, err := (boundTree{expression: predicate.execution}).StructuralOutput(context.Background())
	if err != nil || definitionAfter != definitionBefore || executionAfter != executionBefore || !slices.Equal(predicate.dependencies, manifest) || !slices.Equal(predicate.attributes, attributes) {
		t.Fatal("replacement mutated predicate trees/manifests/layout", err)
	}
	if err := validateIndexDefinition(after, index); err != nil {
		t.Fatal("retained predicate rejected by ordinary exact validation", err)
	}
}
