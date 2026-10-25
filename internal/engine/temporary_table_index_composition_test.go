package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
			var provider, survivor, idx *catalog.Relation
			for _, descriptor := range e.Catalog.State.Descriptors() {
				if d, ok := descriptor.(*catalog.Relation); ok {
					switch d.Name() {
					case "composition_owner":
						provider = d
					case "composition_survivor":
						survivor = d
					case "composition_index":
						idx = d
					}
				}
			}
			if provider == nil || survivor == nil || idx == nil {
				t.Fatal("missing genuine composition fixture")
			}
			originalCatalog := e.Catalog.State
			originalIndex := idx
			if mode != "Ordinary" {
				maximum := *idx
				maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
				if mode == "OrdinaryOverflow" {
					maximum.ObjectRevision--
				}
				idx = &maximum
				e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
				e.Catalog.State.SetDescriptor(idx.Address(), idx)
				t.Cleanup(func() { e.Catalog.PublishLocked(originalCatalog) })
			}
			before := e.Storage.Relations[survivor.Address()]
			oldRows := before.Versions
			oldRoot := e.Storage.IndexRoots[idx.Address()]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			stage.terminalCleanup = mode == "TerminalMaximum"
			changes := session.NewJournal()
			project := func(old, next *catalog.Relation) error {
				_, _, _, err := stagePrivateColumnShape(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: stage.Base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, stage, changes, old, next, nil, nil, visibility.Root{}, false, false)
				return err
			}
			providerRef := e.Catalog.Domain.Ref(stage.Base.Epoch(), provider.Address())
			if err := stage.Remove(providerRef); err != nil {
				t.Fatal(err)
			}
			if err := StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.Address()}, parser.DropCascade, nil, changes, project, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			intermediate := stage.Get(idx.Address()).(*catalog.Relation)
			intermediateParent := stage.Get(survivor.Address()).(*catalog.Relation)
			check := func(candidate, parent *catalog.Relation, wantSlot int, wantLayout []int32) {
				t.Helper()
				derived := candidate.SQLIndex.Keys[0].Derived
				if !slices.Equal(derived.Attributes, wantLayout) {
					t.Fatal("captured layout", derived.Attributes, wantLayout)
				}
				for _, expression := range []expr.Expr{derived.Definition, derived.Execution} {
					node := expression.(plan.ArithmeticExpr).Left
					for {
						cast, ok := node.(plan.CoercionExpr)
						if !ok {
							break
						}
						node = cast.Child
					}
					column := node.(plan.ColumnExpr)
					if column.Slot != wantSlot || column.Origin.Address.SubID != 4 {
						t.Fatal("derived slot/attribute", column.Slot, column.Origin.Address)
					}
				}
				if candidate.Name() != idx.Name() || candidate.Owner != idx.Owner || candidate.Namespace() != idx.Namespace() || candidate.SQLIndex.Ready != idx.SQLIndex.Ready || candidate.SQLIndex.Valid != idx.SQLIndex.Valid || candidate.SQLIndex.Live != idx.SQLIndex.Live {
					t.Fatal("index identity or flags changed")
				}
				if err := index.ValidateIndexDefinition(e.StageRead(stage), candidate); err != nil {
					t.Fatal(err)
				}
				if len(parent.Columns) != len(wantLayout) {
					t.Fatal("parent shape")
				}
			}
			check(intermediate, intermediateParent, 2, []int32{1, 3, 4})
			if intermediate == idx || intermediate.SQLIndex.Keys[0].Derived == idx.SQLIndex.Keys[0].Derived {
				t.Fatal("first candidate mutated original")
			}
			err := stageOwnedExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.Address()}, parser.DropCascade, nil, changes, project)
			if mode == "OrdinaryOverflow" {
				if err == nil || err.Error() != "stuntgres: index revision exhausted" {
					t.Fatal("ordinary second evolution overflow", err)
				}
				if e.Storage.IndexRoots[idx.Address()] != oldRoot || e.Storage.Relations[survivor.Address()] != before {
					t.Fatal("failed evolution changed live roots")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			final := stage.Get(idx.Address()).(*catalog.Relation)
			finalParent := stage.Get(survivor.Address()).(*catalog.Relation)
			check(final, finalParent, 1, []int32{1, 4})
			if final == intermediate || final.SQLIndex.Keys[0].Derived == intermediate.SQLIndex.Keys[0].Derived {
				t.Fatal("second candidate mutated first")
			}
			if !slices.Equal(idx.SQLIndex.Keys[0].Derived.Attributes, []int32{1, 2, 3, 4}) || !slices.Equal(intermediate.SQLIndex.Keys[0].Derived.Attributes, []int32{1, 3, 4}) || len(oldRows[0].Values) != 4 || e.Storage.IndexRoots[idx.Address()] != oldRoot {
				t.Fatal("retained candidate/vector/root mutated")
			}
			for _, retained := range []struct {
				candidate *catalog.Relation
				slot      int
			}{{idx, 3}, {intermediate, 2}} {
				derived := retained.candidate.SQLIndex.Keys[0].Derived
				for _, expression := range []expr.Expr{derived.Definition, derived.Execution} {
					node := expression.(plan.ArithmeticExpr).Left
					for {
						cast, ok := node.(plan.CoercionExpr)
						if !ok {
							break
						}
						node = cast.Child
					}
					if column := node.(plan.ColumnExpr); column.Slot != retained.slot || column.Origin.Address.SubID != 4 {
						t.Fatal("retained derived tree mutated", column.Slot, retained.slot)
					}
				}
			}
			if mode == "TerminalMaximum" && (intermediate.Revision() != idx.Revision() || final.Revision() != idx.Revision()) || mode == "Ordinary" && final.Revision() != idx.Revision()+2 {
				t.Fatal("composition revision frontier")
			}
			e.Mu.Lock()
			err = e.EvolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
			if err == nil {
				e.stageCatalogChangesLocked(stage, changes)
			}
			e.Mu.Unlock()
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
				err = scope.Gate(statement(t, "SELECT 1"))
				if err == nil {
					err = e.prepareCatalogOverlayIndexes(scope, stage, changes)
				}
				_ = scope.Finish(context.Canceled)
				op.Done()
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage.Get(idx.Address()) != final {
				t.Fatal("coherent validation evolved again")
			}
			if changes.Originals[idx.Address()] != idx || changes.Storage[survivor.Address()].Original != before || !storage.SameVersionRoot(changes.Storage[survivor.Address()].Root, oldRows) || changes.Storage[survivor.Address()].Ends != before.Ends || changes.Storage[survivor.Address()].Generation != before.Generation {
				t.Fatal("original publication predecessor lost")
			}
			root := index.JournalIndexesOrZero(changes).UniqueRoots[idx.Address()]
			if root == nil || root == oldRoot || len(root.Entries) != 1 {
				t.Fatal("missing actual rebuilt index root")
			}
			key, kerr := root.Entries[0].Keys[0].Integer()
			if kerr != nil || key != 9 || root.Entries[0].Row != oldRows[0].Row || root.Entries[0].Version != oldRows[0].Version {
				t.Fatal("rebuilt key/identity", key, kerr)
			}
			if originalIndex.SQLIndex.Keys[0].Derived.Attributes[3] != 4 || before != e.Storage.Relations[survivor.Address()] {
				t.Fatal("original fixture changed")
			}
		})
	}
}

func TestTemporaryTableIndexDefinitionOwnNamespace(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, session := range []*session.Session{owner, peer} {
		sessionSQL(t, session, "CREATE TEMP TABLE deparse_owner_table(v INT)")
		sessionSQL(t, session, "CREATE INDEX deparse_owner_index ON deparse_owner_table(v)")
	}
	view := e.CatalogRead()
	var own, foreign *catalog.Relation
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.Name() != "deparse_owner_index" || index.SQLIndex == nil {
			continue
		}
		namespace := view.MustNamespace(index.Namespace())
		if namespace.Temporary == owner.SessionToken {
			own = index
		}
		if namespace.Temporary == peer.SessionToken {
			foreign = index
		}
	}
	if own == nil || foreign == nil {
		t.Fatal("missing actual physical-owner indexes")
	}
	presentation := index.Presentation{Temporary: own.Namespace()}
	actual, err := index.DefinitionSQL(context.Background(), view, own, presentation)
	if err != nil || actual != "CREATE INDEX deparse_owner_index ON pg_temp.deparse_owner_table USING btree (v)" {
		t.Fatal("own namespace alias", actual, err)
	}
	foreignNamespace := view.Get(foreign.Namespace()).Name()
	actual, err = index.DefinitionSQL(context.Background(), view, foreign, presentation)
	if err != nil || actual != "CREATE INDEX deparse_owner_index ON "+foreignNamespace+".deparse_owner_table USING btree (v)" {
		t.Fatal("foreign owner acquired own namespace alias", actual, err)
	}
	presentation.Dump = true
	actual, err = index.DefinitionSQL(context.Background(), view, own, presentation)
	physicalNamespace := view.Get(own.Namespace()).Name()
	expectedPrefix := "CREATE INDEX " + catalog.QuoteIdentifier("deparse_owner_index") + " ON " + catalog.QuoteIdentifier(physicalNamespace) + "." + catalog.QuoteIdentifier("deparse_owner_table") + " USING "
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
	var parent, idx *catalog.Relation
	for _, descriptor := range before.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		switch relation.Name() {
		case "retained_partial":
			parent = relation
		case "retained_partial_index":
			idx = relation
		}
	}
	if parent == nil || idx == nil || idx.SQLIndex.Predicate == nil || idx.SQLIndex.Keys[0].Derived != nil || len(parent.ColumnMeta.Generations) != 0 || len(parent.ColumnMeta.Defaults) != 0 || len(parent.Constraints.Check) != 0 || len(parent.Columns) != 1 || parent.Columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("fixture is not a genuine unmasked plain-key partial predicate")
	}
	predicate := idx.SQLIndex.Predicate
	definitionTypes, executionTypes := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, tree := range []struct {
		node  expr.Expr
		types map[sqltype.ID]*sqltype.Descriptor
	}{
		{predicate.Definition, definitionTypes}, {predicate.Execution, executionTypes},
	} {
		plan.WalkExpression(tree.node, func(node expr.Expr) { typ := node.ResultUsage().Type; tree.types[typ.ID()] = typ })
		if tree.types[sqltype.Bool] == nil || tree.types[sqltype.Int4] == nil {
			t.Fatal("missing actual predicate BOOL/INT4", tree.types)
		}
	}
	definitionBefore, err := (plan.BoundTree{Expression: predicate.Definition}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executionBefore, err := (plan.BoundTree{Expression: predicate.Execution}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := slices.Clone(predicate.Dependencies)
	attributes := slices.Clone(predicate.Attributes)
	store, root := e.Storage.Relations[parent.Address()], e.Storage.IndexRoots[idx.Address()]
	if store == nil || len(store.Versions) != 2 || root == nil || len(root.Entries) != 1 {
		t.Fatal("missing actual rows or partial membership root")
	}
	rows, ends, generation := store.Versions, store.Ends, store.Generation
	// Real foreign-engine replacement manufactures fresh builtin generations;
	// exact TEMP trees/rows/roots must retain theirs, not be rebound to new types.
	if _, err := replaceWithFreshCatalog(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	if after.State() == before.State() || after.Epoch() <= before.Epoch() || after.Get(parent.Address()) != parent || after.Get(idx.Address()) != idx || idx.SQLIndex.Predicate != predicate || e.Storage.Relations[parent.Address()] != store || e.Storage.IndexRoots[idx.Address()] != root || !storage.SameVersionRoot(store.Versions, rows) || store.Ends != ends || store.Generation != generation {
		t.Fatal("replacement changed actual retained catalog/index/store/vector roots")
	}
	for _, tree := range []struct {
		name  string
		node  expr.Expr
		types map[sqltype.ID]*sqltype.Descriptor
	}{
		{"definition", predicate.Definition, definitionTypes}, {"execution", predicate.Execution, executionTypes},
	} {
		for id, typ := range tree.types {
			actual, ok := after.LookupType(id)
			if !ok || actual != typ {
				t.Fatal("predicate concrete type generation not retained", tree.name, id, actual, typ)
			}
		}
		plan.WalkExpression(tree.node, func(node expr.Expr) {
			typ := node.ResultUsage().Type
			if tree.types[typ.ID()] != typ {
				t.Fatal("immutable predicate type rebound", tree.name, typ)
			}
		})
	}
	definitionAfter, err := (plan.BoundTree{Expression: predicate.Definition}).StructuralOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executionAfter, err := (plan.BoundTree{Expression: predicate.Execution}).StructuralOutput(context.Background())
	if err != nil || definitionAfter != definitionBefore || executionAfter != executionBefore || !slices.Equal(predicate.Dependencies, manifest) || !slices.Equal(predicate.Attributes, attributes) {
		t.Fatal("replacement mutated predicate trees/manifests/layout", err)
	}
	if err := index.ValidateIndexDefinition(after, idx); err != nil {
		t.Fatal("retained predicate rejected by ordinary exact validation", err)
	}
}
