package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func temporaryProjectionFixture(t *testing.T) (*Engine, *Session, *catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE shape_provider(v INT)",
		"CREATE TABLE public.shape_survivor(a INT,g BOOL GENERATED ALWAYS AS('shape_provider'::regclass IS NOT NULL) STORED,z INT)",
		"CREATE INDEX shape_external ON public.shape_survivor(('shape_provider'::regclass IS NOT NULL))",
		"CREATE INDEX shape_z_expr ON public.shape_survivor((z+0))",
		"INSERT INTO public.shape_survivor(a,z) VALUES(7,9)",
	} {
		sessionSQL(t, owner, q)
	}
	var provider, survivor, index *catalog.Relation
	for _, d := range e.catalog.state.Descriptors() {
		if r, ok := d.(*catalog.Relation); ok {
			switch r.Name() {
			case "shape_provider":
				provider = r
			case "shape_survivor":
				survivor = r
			case "shape_z_expr":
				index = r
			}
		}
	}
	if provider == nil || survivor == nil || index == nil || len(survivor.ColumnMeta.Generations) != 1 || e.storage.indexRoots[index.Address()] == nil {
		t.Fatal("missing genuine generated/index fixture")
	}
	return e, owner, provider, survivor, index
}

func temporaryProjectionStage(t *testing.T, e *Engine, provider *catalog.Relation) (*CatalogStage, *temporarySessionIndexes) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), provider.Address())
	if err := stage.Remove(ref); err != nil {
		t.Fatal(err)
	}
	if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.Address()}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	indexes, err := e.prepareTemporarySessionIndexes(stage)
	if err != nil {
		t.Fatal(err)
	}
	return stage, indexes
}

func temporaryProjectionInstall(e *Engine, owner *Session, provider *catalog.Relation, stage *CatalogStage, indexes *temporarySessionIndexes) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.installTemporarySessionStageLocked(owner, stage, []ids.ObjectAddress{provider.Namespace()}, indexes)
}

func TestTemporaryTableTerminalShapePredecessors(t *testing.T) {
	for _, mode := range []string{"CurrentRows", "AffectedIndexRoot", "UnrelatedRoot"} {
		t.Run(mode, func(t *testing.T) {
			e, owner, provider, survivor, index := temporaryProjectionFixture(t)
			oldRows := e.storage.relations[survivor.Address()].versions
			stage, indexes := temporaryProjectionStage(t, e, provider)
			captured := e.catalog.state
			var unrelated ids.ObjectAddress
			var unrelatedRoot *logicalIndexRoot
			switch mode {
			case "CurrentRows":
				sessionSQL(t, owner, "INSERT INTO public.shape_survivor(a,z) VALUES(11,13)")
			case "AffectedIndexRoot":
				root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, survivor, index.SQLIndex, e.storage.relations[survivor.Address()], nil)
				if err != nil {
					t.Fatal(err)
				}
				e.storage.indexRoots[index.Address()] = root
			case "UnrelatedRoot":
				sessionSQL(t, owner, "CREATE TABLE public.shape_unrelated(v INT)")
				sessionSQL(t, owner, "CREATE INDEX shape_unrelated_v ON public.shape_unrelated(v)")
				// Reprepare after its catalog creation, then only its row/index publication.
				e.DiscardCatalogStage(stage)
				stage, indexes = temporaryProjectionStage(t, e, provider)
				captured = e.catalog.state
				sessionSQL(t, owner, "INSERT INTO public.shape_unrelated VALUES(99)")
				for address, d := range e.catalog.state.Descriptors() {
					if d.Name() == "shape_unrelated_v" {
						unrelated = address
					}
				}
				unrelatedRoot = e.storage.indexRoots[unrelated]
			}
			if e.catalog.state != captured {
				t.Fatal("fixture changed catalog for a row/root-only fence")
			}
			installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
			if err != nil || installed != (mode == "UnrelatedRoot") {
				t.Fatal("terminal predecessor decision", mode, installed, err)
			}
			if !installed {
				if e.catalog.state != captured || e.catalog.state.Get(survivor.Address()) != survivor || e.catalog.state.Get(provider.Address()) != provider {
					t.Fatal("stale shape mutated catalog")
				}
				e.DiscardCatalogStage(stage)
				stage, indexes = temporaryProjectionStage(t, e, provider)
				installed, err = temporaryProjectionInstall(e, owner, provider, stage, indexes)
				if err != nil || !installed {
					t.Fatal("reprepared shape failed", err)
				}
			}
			next := e.catalog.state.MustRelation(survivor.Address())
			rows := e.storage.relations[survivor.Address()].versions
			if len(next.Columns) != 2 || next.Columns[1].Name != "z" || len(next.ColumnMeta.Generations) != 0 || len(oldRows) != 1 || len(oldRows[0].values) != 3 {
				t.Fatal("shape/snapshot incoherent")
			}
			v, er := rows[0].values[1].Integer()
			if er != nil || v != 9 {
				t.Fatal("projected z", v, er)
			}
			if mode == "CurrentRows" {
				v, er = rows[1].values[1].Integer()
				if er != nil || v != 13 {
					t.Fatal("row-only retry lost newest row", v, er)
				}
			}
			if mode == "UnrelatedRoot" && e.storage.indexRoots[unrelated] != unrelatedRoot {
				t.Fatal("unrelated row/index publication overwritten")
			}
			if e.storage.indexRoots[index.Address()] == indexes.previous[index.Address()] {
				t.Fatal("affected dense-slot index root reused")
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestTemporaryTableTerminalCombinedMaximum(t *testing.T) {
	e, owner, provider, survivor, index := temporaryProjectionFixture(t)
	original := e.catalog.state
	originalStore := e.storage.relations[survivor.Address()]
	retained := originalStore.versions
	maximum := *survivor
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	maximumIndex := *index
	maximumIndex.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	maximumStore := *originalStore
	maximumStore.generation = ^uint64(0)
	e.catalog.state = catalog.CloneState(original)
	e.catalog.state.SetDescriptor(survivor.Address(), &maximum)
	e.catalog.state.SetDescriptor(index.Address(), &maximumIndex)
	e.storage.relations[survivor.Address()] = &maximumStore
	t.Cleanup(func() {
		if e.catalog.state.Get(provider.Address()) != nil {
			e.catalog.state = original
			e.storage.relations[survivor.Address()] = originalStore
		}
	})
	stage, indexes := temporaryProjectionStage(t, e, provider)
	projected := stage.rowShapes[survivor.Address()].replacement
	next := stage.Descriptors[survivor.Address()].(*catalog.Relation)
	nextIndex := stage.Descriptors[index.Address()].(*catalog.Relation)
	if next == &maximum || next.Revision() != maximum.Revision() || nextIndex == &maximumIndex || nextIndex.Revision() != maximumIndex.Revision() || projected == &maximumStore || projected.generation != maximumStore.generation || projected.ends != maximumStore.ends || sameVersionRoot(projected.versions, retained) {
		t.Fatal("terminal maxima require fresh descriptor/store/vector and unchanged history")
	}
	oldRoot := indexes.previous[index.Address()]
	if indexes.roots[index.Address()] == oldRoot {
		t.Fatal("maximum index root reused")
	}
	// A real changed root invalidates the prepared attempt even at equal maxima.
	changedRoot, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, &maximum, maximumIndex.SQLIndex, &maximumStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.storage.indexRoots[index.Address()] = changedRoot
	installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
	if err != nil || installed {
		t.Fatal("maximum stale index attempt installed", installed, err)
	}
	e.storage.indexRoots[index.Address()] = oldRoot
	installed, err = temporaryProjectionInstall(e, owner, provider, stage, indexes)
	if err != nil || !installed {
		t.Fatal("maximum full-stage install", installed, err)
	}
	if e.catalog.nextRevision != maximum.Revision() || len(retained[0].values) != 3 || len(e.storage.relations[survivor.Address()].versions[0].values) != 2 {
		t.Fatal("maximum frontier/retained shape")
	}
	root := e.storage.indexRoots[index.Address()]
	if len(root.entries) != 1 || len(root.entries[0].keys) != 1 {
		t.Fatal("maximum rebuilt index entries")
	}
	key, er := root.entries[0].keys[0].Integer()
	if er != nil || key != 9 {
		t.Fatal("maximum dense-slot index key", key, er)
	}
	value, er := e.storage.relations[survivor.Address()].versions[0].values[1].Integer()
	if er != nil || value != 9 {
		t.Fatal("maximum projected z", value, er)
	}
	if len(maximum.ColumnMeta.Generations) != 1 || len(survivor.ColumnMeta.Generations) != 1 {
		t.Fatal("captured generation mutated")
	}
	e.DiscardCatalogStage(stage)
}

func TestTemporaryTablePrivateProjectionRoots(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, owner, "CREATE TEMP TABLE private_shape_provider(v INT)")
	sessionSQL(t, owner, "CREATE TEMP TABLE private_shape(a INT,g BOOL GENERATED ALWAYS AS('private_shape_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE)")
	sessionSQL(t, owner, "INSERT INTO private_shape(a,z) VALUES(7,9)")
	before := cloneCatalogChanges(owner.transactions.catalog)
	var address ids.ObjectAddress
	for a, d := range before.descriptors {
		if relation, ok := d.(*catalog.Relation); ok && relation.Name() == "private_shape" {
			address = a
			break
		}
	}
	originalRows := owner.transactions.writes[address]
	sessionSQL(t, owner, "SAVEPOINT before_shape")
	sessionSQL(t, owner, "DROP TABLE private_shape_provider CASCADE")
	shape := owner.transactions.catalog.storage[address]
	if shape.original != nil || e.storage.relations[address] != nil || len(shape.relation.Columns) != 2 {
		t.Fatal("invented committed predecessor")
	}
	rows := owner.transactions.writes[address]
	if len(rows) != 1 || len(rows[0].values) != 2 || len(originalRows[0].values) != 3 || rows[0].row != originalRows[0].row || rows[0].version != originalRows[0].version || rows[0].born != originalRows[0].born {
		t.Fatal("private write shape/identity")
	}
	value, err := rows[0].values[1].Integer()
	if err != nil || value != 9 {
		t.Fatal("private projected z", value, err)
	}
	sessionSQL(t, owner, "ROLLBACK TO before_shape")
	if len(owner.transactions.writes[address][0].values) != 3 || owner.transactions.catalog.storage[address].relation != before.storage[address].relation {
		t.Fatal("savepoint shape root not restored")
	}
	sessionSQL(t, owner, "ROLLBACK")
}

func TestTemporaryTableOrdinaryShapeOverflow(t *testing.T) {
	for _, counter := range []string{"Table", "Storage", "Index"} {
		t.Run(counter, func(t *testing.T) {
			e, _, provider, survivor, index := temporaryProjectionFixture(t)
			original := e.catalog.state
			store := e.storage.relations[survivor.Address()]
			maximum := *survivor
			maximumIndex := *index
			maximumStore := *store
			e.catalog.state = catalog.CloneState(original)
			switch counter {
			case "Table":
				maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
				e.catalog.state.SetDescriptor(survivor.Address(), &maximum)
			case "Storage":
				maximumStore.generation = ^uint64(0)
				e.storage.relations[survivor.Address()] = &maximumStore
			case "Index":
				maximumIndex.ObjectRevision = ids.DefinitionRevision(^uint64(0))
				e.catalog.state.SetDescriptor(index.Address(), &maximumIndex)
			}
			captured := e.catalog.state
			t.Cleanup(func() {
				if e.catalog.state.Get(provider.Address()) != nil {
					e.catalog.state = original
					e.storage.relations[survivor.Address()] = store
				}
			})
			stage := e.BeginCatalogStage(e.CatalogRead())
			err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.Address()}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{})
			if err == nil {
				e.mu.Lock()
				err = e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
				e.mu.Unlock()
			}
			want := map[string]string{"Table": "stuntgres: definition revision exhausted", "Storage": "stuntgres: storage generation exhausted", "Index": "stuntgres: index revision exhausted"}[counter]
			if err == nil || err.Error() != want || e.catalog.state != captured {
				t.Fatal("ordinary overflow accepted or altered live catalog", counter, err)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestTemporaryTableTerminalConcurrentIndexFlags(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "NotReady", true: "ReadyInvalid"}[ready], func(t *testing.T) {
			e, owner, provider, survivor, index := temporaryProjectionFixture(t)
			changed := *index
			definition := *index.SQLIndex
			definition.Ready, definition.Valid = ready, false
			changed.SQLIndex = &definition
			e.catalog.state = catalog.CloneState(e.catalog.state)
			e.catalog.state.SetDescriptor(index.Address(), &changed)
			previous := e.storage.indexRoots[index.Address()]
			if !ready {
				delete(e.storage.indexRoots, index.Address())
			}
			stage, indexes := temporaryProjectionStage(t, e, provider)
			installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
			if err != nil || !installed {
				t.Fatal("concurrent flag projection", installed, err)
			}
			next := e.catalog.state.MustRelation(index.Address())
			if next.SQLIndex.Ready != ready || next.SQLIndex.Valid || next.SQLIndex.Live != definition.Live {
				t.Fatal("shape cleanup healed concurrent flags")
			}
			if ready && (e.storage.indexRoots[index.Address()] == nil || e.storage.indexRoots[index.Address()] == previous) || !ready && e.storage.indexRoots[index.Address()] != nil {
				t.Fatal("concurrent root policy changed")
			}
			if len(e.storage.relations[survivor.Address()].versions[0].values) != 2 {
				t.Fatal("concurrent survivor not projected")
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestTemporaryTableRetainedOwnerProjection(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE shape_provider(v INT)",
		"CREATE TEMP TABLE shape_survivor(a INT,g BOOL GENERATED ALWAYS AS('shape_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE)",
		"CREATE INDEX shape_external ON shape_survivor(('shape_provider'::regclass IS NOT NULL))",
		"CREATE INDEX shape_z_idx ON shape_survivor(z)",
		"INSERT INTO shape_survivor(a,z) VALUES(7,9)",
	} {
		sessionSQL(t, owner, q)
	}
	var survivor *catalog.Relation
	for _, descriptor := range e.catalog.state.Descriptors() {
		if table, ok := descriptor.(*catalog.Relation); ok && table.Name() == "shape_survivor" {
			survivor = table
		}
	}
	sessionSQL(t, owner, "INSERT INTO shape_survivor(a,z) VALUES(8,10)")
	frozen := sessionSQL(t, owner, "SELECT * FROM shape_survivor ORDER BY a").Rows.(*finalRows)
	defer frozen.Close()
	original := e.storage.relations[survivor.Address()].versions
	check := func(phase string) {
		t.Helper()
		copy := independentFinalRows(frozen)
		decoded := tuples(Result{Rows: copy})
		if len(decoded) != 2 || decoded[0][1] != true || decoded[0][2] != int64(9) || decoded[1][1] != true || decoded[1][2] != int64(10) {
			t.Fatal(phase, "retained extraction", decoded, frozen.slots)
		}
		for i, want := range []int64{9, 10} {
			row := frozen.source.row(i)
			if len(row) != 3 {
				t.Fatal(phase, "retained source width", len(row))
			}
			generated, err := row[1].Boolean()
			z, zerr := row[2].Integer()
			if err != nil || !generated || zerr != nil || z != want {
				t.Fatal(phase, "retained source", testExport(row[0]), testExport(row[1]), testExport(row[2]), err, zerr)
			}
			if len(original[i].values) != 3 {
				t.Fatal(phase, "original version mutated")
			}
		}
	}
	check("Captured")
	for _, q := range []string{
		"BEGIN",
		"INSERT INTO shape_survivor(a,z) VALUES(11,13)",
		"UPDATE shape_survivor SET z=z+100 WHERE a=7",
		"DELETE FROM shape_survivor WHERE a=8",
		"SAVEPOINT before_shape",
		"DROP TABLE shape_provider CASCADE",
		"SELECT * FROM shape_survivor ORDER BY a",
		"ROLLBACK TO before_shape",
		"ROLLBACK",
	} {
		result := sessionSQL(t, owner, q)
		if result.Rows != nil {
			result.Rows.Close()
		}
		check(q)
	}
}
