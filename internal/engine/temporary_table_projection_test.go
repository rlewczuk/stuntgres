package engine

import (
	"context"
	"stuntgres/internal/parser"
	"testing"
)

func temporaryProjectionFixture(t *testing.T) (*Engine, *Session, *relationDescriptor, *relationDescriptor, *relationDescriptor) {
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
	var provider, survivor, index *relationDescriptor
	for _, d := range e.state.descriptors {
		if r, ok := d.(*relationDescriptor); ok {
			switch r.name {
			case "shape_provider":
				provider = r
			case "shape_survivor":
				survivor = r
			case "shape_z_expr":
				index = r
			}
		}
	}
	if provider == nil || survivor == nil || index == nil || len(survivor.generations) != 1 || e.indexRoots[index.address] == nil {
		t.Fatal("missing genuine generated/index fixture")
	}
	return e, owner, provider, survivor, index
}

func temporaryProjectionStage(t *testing.T, e *Engine, provider *relationDescriptor) (*CatalogStage, *temporarySessionIndexes) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: provider.address}
	if err := stage.Remove(ref); err != nil {
		t.Fatal(err)
	}
	if err := stageExpressionDependencies(context.Background(), stage, []ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil, ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	indexes, err := e.prepareTemporarySessionIndexes(stage)
	if err != nil {
		t.Fatal(err)
	}
	return stage, indexes
}

func temporaryProjectionInstall(e *Engine, owner *Session, provider *relationDescriptor, stage *CatalogStage, indexes *temporarySessionIndexes) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.installTemporarySessionStageLocked(owner, stage, []ObjectAddress{provider.namespace}, indexes)
}

func TestTemporaryTableTerminalShapePredecessors(t *testing.T) {
	for _, mode := range []string{"CurrentRows", "AffectedIndexRoot", "UnrelatedRoot"} {
		t.Run(mode, func(t *testing.T) {
			e, owner, provider, survivor, index := temporaryProjectionFixture(t)
			oldRows := e.storage[survivor.address].versions
			stage, indexes := temporaryProjectionStage(t, e, provider)
			captured := e.state
			var unrelated ObjectAddress
			var unrelatedRoot *logicalIndexRoot
			switch mode {
			case "CurrentRows":
				sessionSQL(t, owner, "INSERT INTO public.shape_survivor(a,z) VALUES(11,13)")
			case "AffectedIndexRoot":
				root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, survivor, index.sqlIndex, e.storage[survivor.address], nil)
				if err != nil {
					t.Fatal(err)
				}
				e.indexRoots[index.address] = root
			case "UnrelatedRoot":
				sessionSQL(t, owner, "CREATE TABLE public.shape_unrelated(v INT)")
				sessionSQL(t, owner, "CREATE INDEX shape_unrelated_v ON public.shape_unrelated(v)")
				// Reprepare after its catalog creation, then only its row/index publication.
				e.DiscardCatalogStage(stage)
				stage, indexes = temporaryProjectionStage(t, e, provider)
				captured = e.state
				sessionSQL(t, owner, "INSERT INTO public.shape_unrelated VALUES(99)")
				for address, d := range e.state.descriptors {
					if d.Name() == "shape_unrelated_v" {
						unrelated = address
					}
				}
				unrelatedRoot = e.indexRoots[unrelated]
			}
			if e.state != captured {
				t.Fatal("fixture changed catalog for a row/root-only fence")
			}
			installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
			if err != nil || installed != (mode == "UnrelatedRoot") {
				t.Fatal("terminal predecessor decision", mode, installed, err)
			}
			if !installed {
				if e.state != captured || e.state.descriptors[survivor.address] != survivor || e.state.descriptors[provider.address] != provider {
					t.Fatal("stale shape mutated catalog")
				}
				e.DiscardCatalogStage(stage)
				stage, indexes = temporaryProjectionStage(t, e, provider)
				installed, err = temporaryProjectionInstall(e, owner, provider, stage, indexes)
				if err != nil || !installed {
					t.Fatal("reprepared shape failed", err)
				}
			}
			next := e.state.descriptors[survivor.address].(*relationDescriptor)
			rows := e.storage[survivor.address].versions
			if len(next.columns) != 2 || next.columns[1].Name != "z" || len(next.generations) != 0 || len(oldRows) != 1 || len(oldRows[0].values) != 3 {
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
			if mode == "UnrelatedRoot" && e.indexRoots[unrelated] != unrelatedRoot {
				t.Fatal("unrelated row/index publication overwritten")
			}
			if e.indexRoots[index.address] == indexes.previous[index.address] {
				t.Fatal("affected dense-slot index root reused")
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestTemporaryTableTerminalCombinedMaximum(t *testing.T) {
	e, owner, provider, survivor, index := temporaryProjectionFixture(t)
	original := e.state
	originalStore := e.storage[survivor.address]
	retained := originalStore.versions
	maximum := *survivor
	maximum.revision = DefinitionRevision(^uint64(0))
	maximumIndex := *index
	maximumIndex.revision = DefinitionRevision(^uint64(0))
	maximumStore := *originalStore
	maximumStore.generation = ^uint64(0)
	e.state = cloneCatalog(original)
	e.state.descriptors[survivor.address] = &maximum
	e.state.descriptors[index.address] = &maximumIndex
	e.storage[survivor.address] = &maximumStore
	t.Cleanup(func() {
		if e.state.descriptors[provider.address] != nil {
			e.state = original
			e.storage[survivor.address] = originalStore
		}
	})
	stage, indexes := temporaryProjectionStage(t, e, provider)
	projected := stage.rowShapes[survivor.address].replacement
	next := stage.descriptors[survivor.address].(*relationDescriptor)
	nextIndex := stage.descriptors[index.address].(*relationDescriptor)
	if next == &maximum || next.revision != maximum.revision || nextIndex == &maximumIndex || nextIndex.revision != maximumIndex.revision || projected == &maximumStore || projected.generation != maximumStore.generation || projected.ends != maximumStore.ends || sameVersionRoot(projected.versions, retained) {
		t.Fatal("terminal maxima require fresh descriptor/store/vector and unchanged history")
	}
	oldRoot := indexes.previous[index.address]
	if indexes.roots[index.address] == oldRoot {
		t.Fatal("maximum index root reused")
	}
	// A real changed root invalidates the prepared attempt even at equal maxima.
	changedRoot, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, &maximum, maximumIndex.sqlIndex, &maximumStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.indexRoots[index.address] = changedRoot
	installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
	if err != nil || installed {
		t.Fatal("maximum stale index attempt installed", installed, err)
	}
	e.indexRoots[index.address] = oldRoot
	installed, err = temporaryProjectionInstall(e, owner, provider, stage, indexes)
	if err != nil || !installed {
		t.Fatal("maximum full-stage install", installed, err)
	}
	if e.nextRevision != maximum.revision || len(retained[0].values) != 3 || len(e.storage[survivor.address].versions[0].values) != 2 {
		t.Fatal("maximum frontier/retained shape")
	}
	root := e.indexRoots[index.address]
	if len(root.entries) != 1 || len(root.entries[0].keys) != 1 {
		t.Fatal("maximum rebuilt index entries")
	}
	key, er := root.entries[0].keys[0].Integer()
	if er != nil || key != 9 {
		t.Fatal("maximum dense-slot index key", key, er)
	}
	value, er := e.storage[survivor.address].versions[0].values[1].Integer()
	if er != nil || value != 9 {
		t.Fatal("maximum projected z", value, er)
	}
	if len(maximum.generations) != 1 || len(survivor.generations) != 1 {
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
	var address ObjectAddress
	for a, d := range before.descriptors {
		if relation, ok := d.(*relationDescriptor); ok && relation.name == "private_shape" {
			address = a
			break
		}
	}
	originalRows := owner.transactions.writes[address]
	sessionSQL(t, owner, "SAVEPOINT before_shape")
	sessionSQL(t, owner, "DROP TABLE private_shape_provider CASCADE")
	shape := owner.transactions.catalog.storage[address]
	if shape.original != nil || e.storage[address] != nil || len(shape.relation.columns) != 2 {
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
			original := e.state
			store := e.storage[survivor.address]
			maximum := *survivor
			maximumIndex := *index
			maximumStore := *store
			e.state = cloneCatalog(original)
			switch counter {
			case "Table":
				maximum.revision = DefinitionRevision(^uint64(0))
				e.state.descriptors[survivor.address] = &maximum
			case "Storage":
				maximumStore.generation = ^uint64(0)
				e.storage[survivor.address] = &maximumStore
			case "Index":
				maximumIndex.revision = DefinitionRevision(^uint64(0))
				e.state.descriptors[index.address] = &maximumIndex
			}
			captured := e.state
			t.Cleanup(func() {
				if e.state.descriptors[provider.address] != nil {
					e.state = original
					e.storage[survivor.address] = store
				}
			})
			stage := e.BeginCatalogStage(e.CatalogRead())
			err := stageExpressionDependencies(context.Background(), stage, []ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil, ObjectAddress{})
			if err == nil {
				e.mu.Lock()
				err = e.evolveStageIndexesLocked(stage, ObjectAddress{}, nil)
				e.mu.Unlock()
			}
			want := map[string]string{"Table": "stuntgres: definition revision exhausted", "Storage": "stuntgres: storage generation exhausted", "Index": "stuntgres: index revision exhausted"}[counter]
			if err == nil || err.Error() != want || e.state != captured {
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
			definition := *index.sqlIndex
			definition.ready, definition.valid = ready, false
			changed.sqlIndex = &definition
			e.state = cloneCatalog(e.state)
			e.state.descriptors[index.address] = &changed
			previous := e.indexRoots[index.address]
			if !ready {
				delete(e.indexRoots, index.address)
			}
			stage, indexes := temporaryProjectionStage(t, e, provider)
			installed, err := temporaryProjectionInstall(e, owner, provider, stage, indexes)
			if err != nil || !installed {
				t.Fatal("concurrent flag projection", installed, err)
			}
			next := e.state.descriptors[index.address].(*relationDescriptor)
			if next.sqlIndex.ready != ready || next.sqlIndex.valid || next.sqlIndex.live != definition.live {
				t.Fatal("shape cleanup healed concurrent flags")
			}
			if ready && (e.indexRoots[index.address] == nil || e.indexRoots[index.address] == previous) || !ready && e.indexRoots[index.address] != nil {
				t.Fatal("concurrent root policy changed")
			}
			if len(e.storage[survivor.address].versions[0].values) != 2 {
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
	var survivor *relationDescriptor
	for _, descriptor := range e.state.descriptors {
		if table, ok := descriptor.(*relationDescriptor); ok && table.name == "shape_survivor" {
			survivor = table
		}
	}
	sessionSQL(t, owner, "INSERT INTO shape_survivor(a,z) VALUES(8,10)")
	frozen := sessionSQL(t, owner, "SELECT * FROM shape_survivor ORDER BY a").Rows.(*finalRows)
	defer frozen.Close()
	original := e.storage[survivor.address].versions
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
