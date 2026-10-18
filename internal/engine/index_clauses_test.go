package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func indexClausesTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func indexClausesFixture(t *testing.T) (*relationDescriptor, *relationDescriptor, *catalogState, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	types := indexClausesTypes()
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: "t", revision: 1, relationKind: tableRelation, attributes: []int32{10, 20, 30}, attributeHighWater: 30, columns: []ColumnDescriptor{{Name: "a", Usage: types[sqltype.Int4].Usage()}, {Name: "b", Usage: types[sqltype.Int4].Usage()}, {Name: "p", Usage: types[sqltype.Text].Usage()}}, index: map[string]int{"a": 0, "b": 1, "p": 2}}
	c, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	def := &indexDefinition{parent: parent.address, keys: []indexKey{{attribute: 10, class: c}, {attribute: 20, class: c, desc: true, nullsFirst: true}}, include: []int32{30}, ready: true, valid: true, live: true}
	index := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, namespace: parent.namespace, name: "cover", revision: 1, relationKind: indexRelation, sqlIndex: def, columns: append([]ColumnDescriptor(nil), parent.columns...), attributes: []int32{1, 2, 3}, attributeHighWater: 3, index: map[string]int{"a": 0, "b": 1, "p": 2}}
	catalog := &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{parent.address: parent, index.address: index}}
	if err := validateIndexDefinition(CatalogRead{state: catalog}, index); err != nil {
		t.Fatal(err)
	}
	return parent, index, catalog, types
}

func TestIndexClausesRootAttributes(t *testing.T) {
	parent, index, _, types := indexClausesFixture(t)
	ctx := context.Background()
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(types[sqltype.Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	store := &relationStorage{versions: []rowVersion{
		{row: 1, version: 1, born: 1, values: []sqltype.Value{integer(1), integer(2), sqltype.String(types[sqltype.Text], "x")}},
		{row: 2, version: 2, born: 1, values: []sqltype.Value{integer(1), sqltype.Null(types[sqltype.Int4]), sqltype.String(types[sqltype.Text], "z")}},
		{row: 3, version: 3, born: 1, values: []sqltype.Value{integer(1), integer(2), sqltype.Null(types[sqltype.Text])}},
	}}
	root, err := buildIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, parent, index.sqlIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.entries) != 3 || root.entries[0].row != 2 || root.entries[1].row != 1 || root.entries[2].row != 3 {
		t.Fatal(root)
	}
	for _, entry := range root.entries {
		if len(entry.keys) != 2 {
			t.Fatal("INCLUDE entered logical key", entry)
		}
	}
	ordering, err := bindIndexOrdering(parent, index.sqlIndex)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := ordering.compareKeys(ctx, root.entries[1], root.entries[2]); err != nil || cmp != 0 {
		t.Fatal("payload affected equality", cmp, err)
	}
	successor := store.versions[0]
	successor.base = 1
	successor.version = 4
	successor.values = []sqltype.Value{integer(1), integer(2), sqltype.String(types[sqltype.Text], "changed")}
	merged, err := mergeIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, parent, index, root.entries, &rowPublication{writes: []rowVersion{successor}}, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.entries) != 3 || merged.entries[1].version != 4 || root.entries[1].version != 1 || len(merged.entries[1].keys) != 2 {
		t.Fatal("immutable payload-only merge", root, merged)
	}
	// Included NAME[] has storage but no ordering provider.
	domain, err := sqltype.Domain(21000, "payload", "public.payload", types[sqltype.NameArray].Usage())
	if err != nil {
		t.Fatal(err)
	}
	next := *parent
	next.columns = append([]ColumnDescriptor(nil), parent.columns...)
	next.columns[2].Usage = domain.Usage()
	evolved, err := evolveIndex(index, parent, &next, []columnTransformation{{attribute: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if evolved.columns[2].Usage.Type != domain || len(evolved.sqlIndex.keys) != 2 {
		t.Fatal(evolved)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ordering.compareKeys(canceled, root.entries[1], root.entries[2]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := buildIndexRoot(&EvalContext{Context: canceled, Scope: 1, Frame: 1}, parent, index.sqlIndex, store, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestIndexClausesClassRoots(t *testing.T) {
	parent, index, catalog, types := indexClausesFixture(t)
	ctx := context.Background()
	oid, _ := sqltype.LookupIndexClass("oid_ops")
	index.sqlIndex = &indexDefinition{parent: parent.address, keys: []indexKey{{attribute: 10, class: oid, explicitClass: true}}, include: []int32{30}, ready: true, valid: true, live: true}
	index.columns = []ColumnDescriptor{parent.columns[0], parent.columns[2]}
	index.attributes = []int32{1, 2}
	index.attributeHighWater = 2
	if err := validateIndexDefinition(CatalogRead{state: catalog}, index); err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	negative, _ := sqltype.Integer(types[sqltype.Int4], -1)
	store := &relationStorage{versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{negative, one, sqltype.Null(types[sqltype.Text])}}, {row: 2, version: 2, born: 1, values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "p")}}}}
	root, err := buildIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, parent, index.sqlIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.entries[0].row != 2 || root.entries[1].row != 1 {
		t.Fatal("oid_ops did not choose unsigned provider", root)
	}
	bpchar, _ := sqltype.LookupIndexClass("bpchar_ops")
	def := &indexDefinition{parent: parent.address, keys: []indexKey{{attribute: 30, class: bpchar, explicitClass: true, collation: 950}}, ready: true, valid: true, live: true}
	ordering, err := bindIndexOrdering(parent, def)
	if err != nil {
		t.Fatal(err)
	}
	a := logicalIndexEntry{keys: []sqltype.Value{sqltype.String(types[sqltype.Text], "a")}}
	b := logicalIndexEntry{keys: []sqltype.Value{sqltype.String(types[sqltype.Text], "a ")}}
	if cmp, err := ordering.compareKeys(ctx, a, b); err != nil || cmp != 0 {
		t.Fatal("bpchar class lost padding semantics", cmp, err)
	}
}

func TestIndexClausesUniquePayload(t *testing.T) {
	parent, index, catalog, types := indexClausesFixture(t)
	ctx := context.Background()
	c := uniqueConstraint{OID: 22000, Name: "cover", Attributes: []int32{10}, Index: index.address}
	parent.uniqueConstraints = []uniqueConstraint{c}
	class, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	index.sqlIndex = &indexDefinition{parent: parent.address, keys: []indexKey{{attribute: 10, class: class}}, include: []int32{30}, unique: true, nullsNotDistinct: c.NullsNotDistinct, constraint: uniqueBacking(&c), ready: true, valid: true, live: true}
	index.columns = []ColumnDescriptor{parent.columns[0], parent.columns[2]}
	index.attributes = []int32{1, 2}
	index.attributeHighWater = 2
	if err := validateIndexDefinition(CatalogRead{state: catalog}, index); err != nil {
		t.Fatal(err)
	}
	if err := validateUniques(CatalogRead{state: catalog}, parent, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	for _, payload := range []sqltype.Value{sqltype.String(types[sqltype.Text], "different"), sqltype.Null(types[sqltype.Text])} {
		store := &relationStorage{versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "first")}}, {row: 2, version: 2, born: 1, values: []sqltype.Value{one, one, payload}}}}
		root, err := buildIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, parent, index.sqlIndex, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(root.entries) != 2 || len(root.entries[0].keys) != 1 {
			t.Fatal(root)
		}
		err = validateUniqueRoot(ctx, parent, index, root, "public", sqltype.OutputEnvironment{}, nil, false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Detail != "Key (a)=(1) already exists." {
			t.Fatal("INCLUDE weakened actual unique root", err)
		}
	}
}

func TestIndexClausesEvolution(t *testing.T) {
	parent, index, catalog, types := indexClausesFixture(t)
	index.sqlIndex.keys[0].explicitClass = true
	textPattern, _ := sqltype.LookupIndexClass("text_pattern_ops")
	index.sqlIndex.keys[1] = indexKey{attribute: 30, class: textPattern, explicitClass: true, collation: 951}
	index.sqlIndex.include = []int32{20}
	index.columns = []ColumnDescriptor{parent.columns[0], parent.columns[2], parent.columns[1]}
	index.columns[1].Usage = indexKeyCollation(parent.columns[2].Usage, index.sqlIndex.keys[1])
	if err := validateIndexDefinition(CatalogRead{state: catalog}, index); err != nil {
		t.Fatal(err)
	}
	// Included TYPE normalizes every OLD default, not only the changed key.
	next := *parent
	next.columns = append([]ColumnDescriptor(nil), parent.columns...)
	next.columns[1].Usage = types[sqltype.Text].Usage()
	evolved, err := evolveIndex(index, parent, &next, []columnTransformation{{attribute: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if evolved.sqlIndex == index.sqlIndex || evolved.sqlIndex.keys[0].explicitClass || !index.sqlIndex.keys[0].explicitClass || evolved.sqlIndex.keys[1].class != textPattern || evolved.sqlIndex.keys[1].collation != 951 {
		t.Fatal("whole-index reconstruction/source immutability", index, evolved)
	}
	newer := next
	newer.columns = append([]ColumnDescriptor(nil), next.columns...)
	newer.columns[0].Usage = types[sqltype.Text].Usage()
	rebuilt, err := evolveIndex(evolved, &next, &newer, []columnTransformation{{attribute: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.sqlIndex.keys[0].class.Name != "text_ops" || rebuilt.columns[0].Usage.Type != types[sqltype.Text] {
		t.Fatal(rebuilt)
	}
	oid, _ := sqltype.LookupIndexClass("oid_ops")
	retained := *index
	def := *index.sqlIndex
	def.keys = append([]indexKey(nil), def.keys...)
	def.keys[0].class = oid
	retained.sqlIndex = &def
	_, err = evolveIndex(&retained, parent, &newer, []columnTransformation{{attribute: 10}})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42804" || retained.sqlIndex.keys[0].class != oid {
		t.Fatal("nondefault class was replaced", err)
	}
	dropped := *parent
	dropped.attributes = []int32{10, 30}
	dropped.columns = []ColumnDescriptor{parent.columns[0], parent.columns[2]}
	if removed, err := evolveIndex(index, parent, &dropped, nil); err != nil || removed != nil {
		t.Fatal("included DROP retained dependency", removed, err)
	}
	moved := *parent
	moved.namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 23000}
	movedIndex, err := evolveIndex(index, parent, &moved, nil)
	if err != nil || movedIndex.sqlIndex != index.sqlIndex {
		t.Fatal("schema move reconstructed options", err)
	}
}

func TestIndexClausesSameTypeEvolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attribute int32
		rewrite   bool
	}{
		{"key", 10, false},
		{"key-using", 10, true},
		{"include", 30, false},
		{"include-using", 30, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, index, catalog, types := indexClausesFixture(t)
			parent.columns[1].Usage = types[sqltype.Text].Usage()
			textClass, _ := sqltype.DefaultIndexClass(types[sqltype.Text])
			index.sqlIndex.keys[0].explicitClass = true
			index.sqlIndex.keys[1].class = textClass
			index.sqlIndex.keys[1].explicitClass = true
			index.sqlIndex.keys[1].collation = parent.columns[1].Usage.Collation.ID
			index.columns[1].Usage = indexKeyCollation(parent.columns[1].Usage, index.sqlIndex.keys[1])
			if err := validateIndexDefinition(CatalogRead{state: catalog}, index); err != nil {
				t.Fatal(err)
			}
			definition := index.sqlIndex
			keys := append([]indexKey(nil), definition.keys...)
			columns := append([]ColumnDescriptor(nil), index.columns...)
			parentColumns := append([]ColumnDescriptor(nil), parent.columns...)
			e := New()
			stage := e.BeginCatalogStage(e.catalog.read(catalog))
			if err := stage.Replace(parent); err != nil {
				t.Fatal(err)
			}
			transforms := []columnTransformation{{attribute: tc.attribute, rewrite: tc.rewrite}}
			if err := e.evolveStageIndexesLocked(stage, parent.address, transforms); err != nil {
				t.Fatal(err)
			}
			evolved, ok := stage.descriptors[index.address].(*relationDescriptor)
			if !ok || evolved == index || evolved.sqlIndex == definition || evolved.revision != index.revision+1 {
				t.Fatal("same-Usage TYPE did not detach dependent definition", evolved)
			}
			for i, key := range evolved.sqlIndex.keys {
				if key.explicitClass || key.collation != 0 || key.class != keys[i].class || key.desc != keys[i].desc || key.nullsFirst != keys[i].nullsFirst {
					t.Fatal("whole-index default normalization lost key semantics", key)
				}
			}
			if index.sqlIndex != definition || !reflect.DeepEqual(definition.keys, keys) || !reflect.DeepEqual(index.columns, columns) || !reflect.DeepEqual(parent.columns, parentColumns) {
				t.Fatal("TYPE evolution mutated original descriptor")
			}
			if err := validateIndexDefinition(CatalogRead{state: catalog}, evolved); err != nil {
				t.Fatal(err)
			}
			for _, changes := range [][]columnTransformation{nil, {{attribute: 99}}} {
				unchanged, err := evolveIndex(index, parent, parent, changes)
				if err != nil || unchanged != index {
					t.Fatal("nondependent operation normalized defaults", unchanged, err)
				}
			}
			other := parent.address
			other.ObjectID += 2
			unrelated := e.BeginCatalogStage(e.catalog.read(catalog))
			if err := unrelated.Replace(parent); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(unrelated, other, transforms); err != nil || unrelated.descriptors[index.address] != nil {
				t.Fatal("TYPE attribute leaked across parent identity", err)
			}
		})
	}
}

func TestIndexClausesUntouchedStage(t *testing.T) {
	parent, index, catalog, _ := indexClausesFixture(t)
	e := New()
	stage := e.BeginCatalogStage(e.catalog.read(catalog))
	transforms := []columnTransformation{{attribute: parent.attributes[0]}}
	var err error
	allocations := testing.AllocsPerRun(100, func() {
		err = e.evolveStageIndexesLocked(stage, parent.address, transforms)
	})
	if err != nil || allocations != 0 || len(stage.descriptors) != 0 || catalog.Get(index.address) != index {
		t.Fatal("untouched parent evolution allocated or changed descriptors", allocations, err)
	}
}

func TestIndexClausesReplacement(t *testing.T) {
	parent, index, catalog, types := indexClausesFixture(t)
	ctx := context.Background()
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	source := &relationStorage{versions: []rowVersion{{row: 1, version: 2, born: 1, values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}
	original, err := buildIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, parent, index.sqlIndex, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := &relationStorage{versions: append([]rowVersion(nil), source.versions...)}
	e := New()
	e.nextRow = 100
	e.nextVersion = 200
	e.catalog.state = catalog
	e.storage[parent.address] = source
	e.indexRoots = map[ids.ObjectAddress]*logicalIndexRoot{index.address: original}
	e.commitSeq = 1
	snapshot, err := e.captureIndexSnapshot(ctx, e.catalog.read(catalog), map[ids.ObjectAddress]*relationStorage{parent.address: source}, map[ids.ObjectAddress]*logicalIndexRoot{index.address: original}, 1)
	if err != nil {
		t.Fatal(err)
	}
	roots, rows, versions, err := e.prepareRemintedStorage(ctx, catalog, map[ids.ObjectAddress]*relationStorage{parent.address: destination}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 101 || versions != 201 || roots[index.address].entries[0].row != 101 || len(roots[index.address].entries[0].keys) != 2 || e.nextRow != 100 || e.nextVersion != 200 {
		t.Fatal("replacement did not privately remint actual key-only root", roots, rows, versions)
	}
	if !reflect.DeepEqual(index.sqlIndex.include, []int32{30}) || source.versions[0].row != 1 || original.entries[0].version != 2 {
		t.Fatal("replacement mutated source")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := e.prepareRemintedStorage(canceled, catalog, map[ids.ObjectAddress]*relationStorage{parent.address: destination}, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.nextRow != 100 || e.nextVersion != 200 {
		t.Fatal("failed replacement published allocator")
	}
}

func TestIndexClausesValidationOnlyReplacement(t *testing.T) {
	parent, index, catalog, types := indexClausesFixture(t)
	one, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	storage := map[ids.ObjectAddress]*relationStorage{parent.address: {versions: []rowVersion{{row: 1, version: 2, born: 1, values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}}
	e := New()
	// This is a deliberately reduced but valid key-construction catalogue:
	// namespace descriptors are needed for UNIQUE diagnostics, not nonunique
	// ordering/provider/shape validation or replacement materialization.
	roots, err := e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, catalog, catalog, storage, nil, nil, nil, nil, false, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots != nil {
		t.Fatal("validation-only replacement required unrelated namespace metadata or materialized roots", roots, err)
	}
	broken := *index
	definition := *index.sqlIndex
	definition.keys = append([]indexKey(nil), index.sqlIndex.keys...)
	definition.keys[0].attribute = 999
	broken.sqlIndex = &definition
	invalid := &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{parent.address: parent, index.address: &broken}}
	for _, materialize := range []bool{false, true} {
		if _, err := e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, catalog, invalid, storage, nil, nil, nil, nil, materialize, sqltype.OutputEnvironment{}, nil, nil, nil); err == nil {
			t.Fatal("phase switch weakened actual index definition validation", materialize)
		}
	}
}

func TestIndexClausesDerivedProviders(t *testing.T) {
	parent, _, _, types := indexClausesFixture(t)
	typ := types[sqltype.Text]
	usage := typ.Usage()
	column := columnExpression{Info: expressionInfo{Usage: usage, Property: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 2}
	plan, err := sqltype.BindString("lower", usage, usage, []sqltype.Usage{usage}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	node := functions.String{Info: column.Info, Plan: plan, Args: []BoundExpr{column}, Textual: [4]bool{true}}
	class, _ := sqltype.LookupIndexClass("bpchar_ops")
	definition := &indexDefinition{parent: parent.address, keys: []indexKey{{derived: &derivedIndexKey{definition: node, execution: node, attributes: parent.attributes}, class: class, explicitClass: true, collation: 950}}, include: []int32{10}, ready: true, valid: true, live: true}
	store := &relationStorage{}
	for i, text := range []string{"Alpha ", "ALPHA"} {
		payload, err := sqltype.Integer(types[sqltype.Int4], int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		store.versions = append(store.versions, rowVersion{row: ids.RowID(i + 1), version: ids.VersionID(i + 1), values: []sqltype.Value{payload, payload, sqltype.String(typ, text)}})
	}
	root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.entries) != 2 || root.entries[0].row != 1 || root.entries[1].row != 2 {
		t.Fatal("derived key used default TEXT ordering instead of padding semantics", root)
	}
	ordering, err := bindIndexOrdering(parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := ordering.compareKeys(context.Background(), root.entries[0], root.entries[1]); err != nil || cmp != 0 {
		t.Fatal("derived explicit class did not own equality", cmp, err)
	}
	effective, err := indexKeyUsage(parent, definition.keys[0])
	if err != nil || effective.Type != typ || effective.Collation.ID != 950 || parent.columns[2].Usage != usage {
		t.Fatal("derived effective Usage mutated result or parent identity", effective, err)
	}
	for i, want := range []string{"alpha ", "alpha"} {
		entry := root.entries[i]
		text, err := entry.keys[0].Text()
		if err != nil || len(entry.keys) != 1 || entry.keys[0].Type() != typ || text != want {
			t.Fatal("derived comparison rewrote value typing or copied INCLUDE payload", entry, err)
		}
	}
	text, err := store.versions[0].values[2].Text()
	if err != nil || text != "Alpha " {
		t.Fatal("derived comparison mutated original storage", text, err)
	}
}

// Public SQL execution and diagnostics live in reference/. This unit invokes
// the real parser/binder, TYPE queue, evolution and root builders directly.
func TestIndexClausesBoundDerivedEvolution(t *testing.T) {
	for _, dependencyType := range []bool{false, true} {
		t.Run(map[bool]string{false: "include-type", true: "dependency-type"}[dependencyType], func(t *testing.T) {
			e, scope, auth, parent := alterAddUnitScope(t)
			ctx := context.Background()
			integer, text, varchar := testUsage(e, sqltype.Int4), testUsage(e, sqltype.Text), testUsage(e, sqltype.VarChar)
			parent.relationKind = tableRelation
			parent.attributes = []int32{1, 2, 3, 4}
			parent.attributeHighWater = 4
			parent.columns = []ColumnDescriptor{{Name: "extra", Usage: integer}, {Name: "n", Usage: integer}, {Name: "s", Usage: text}, {Name: "p", Usage: text}}
			parent.index = map[string]int{"extra": 0, "n": 1, "s": 2, "p": 3}
			for i := range parent.columns {
				parent.columns[i].Nullable, parent.columns[i].NullabilityKnown = true, true
				parent.columns[i].OriginTable, parent.columns[i].OriginAttribute = 20000, int16(i+1)
			}
			setup := e.BeginCatalogStage(auth.catalog)
			if err := setup.Create(parent); err != nil {
				t.Fatal(err)
			}
			auth.catalog = e.stageRead(setup)
			auth.recaptureCatalog(auth.catalog)
			bind := func(name, query string, oid ids.OID) *relationDescriptor {
				raw, err := parser.Parse(query)
				if err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				definition, columns, attributes, labels, err := e.bindIndexAttributes(scope.op, scope.input, auth, parent, raw[0].(*parser.CreateIndex))
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				index := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, namespace: parent.namespace, owner: parent.owner, name: name, revision: 1, relationKind: indexRelation, sqlIndex: definition, columns: columns, attributes: attributes, attributeHighWater: int32(len(columns)), index: labels}
				if err := setup.Create(index); err != nil {
					t.Fatal(err)
				}
				return index
			}
			unsigned := bind("unsigned", "CREATE INDEX unsigned ON t((n+0) oid_ops) INCLUDE(p)", 20001)
			cover := bind("cover", `CREATE INDEX cover ON t(lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST,lower(s) COLLATE "default" text_ops) INCLUDE(p)`, 20002)
			view := e.stageRead(setup)
			auth.catalog = view
			auth.recaptureCatalog(auth.catalog)
			for _, index := range []*relationDescriptor{unsigned, cover} {
				if err := validateIndexDefinition(view, index); err != nil {
					t.Fatal(err)
				}
			}
			value := func(n int64) sqltype.Value {
				v, err := sqltype.Integer(integer.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			store := &relationStorage{versions: []rowVersion{
				{row: 1, version: 1, values: []sqltype.Value{value(0), value(-1), sqltype.String(text.Type, "a "), sqltype.String(text.Type, "space")}},
				{row: 2, version: 2, values: []sqltype.Value{value(0), value(1), sqltype.String(text.Type, "a"), sqltype.String(text.Type, "plain")}},
			}}
			root := func(owner, index *relationDescriptor, storage *relationStorage) *logicalIndexRoot {
				r, err := buildIndexRoot(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, owner, index.sqlIndex, storage, nil)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			unsignedRoot, coverRoot := root(parent, unsigned, store), root(parent, cover, store)
			if unsignedRoot.entries[0].row != 2 || unsignedRoot.entries[1].row != 1 || len(unsignedRoot.entries[0].keys) != 1 || unsignedRoot.entries[0].keys[0].Type() != integer.Type {
				t.Fatal("actual bound derived key lost unsigned semantics or original typing", unsignedRoot)
			}
			padding, err := bindIndexOrdering(parent, cover.sqlIndex)
			if err != nil {
				t.Fatal(err)
			}
			if cmp, err := padding.comparisons[0].Compare(ctx, coverRoot.entries[0].keys[0], coverRoot.entries[1].keys[0], scope.input); err != nil || cmp != 0 {
				t.Fatal("actual bound lower(TEXT) lost padding peers", cmp, err)
			}
			if cmp, err := padding.comparisons[1].Compare(ctx, coverRoot.entries[0].keys[1], coverRoot.entries[1].keys[1], scope.input); err != nil || cmp >= 0 {
				t.Fatal("ordinary TEXT distinction was lost", cmp, err)
			}
			snapshot := func(index *relationDescriptor) []string {
				var result []string
				for _, key := range index.sqlIndex.keys {
					for _, node := range []BoundExpr{key.derived.definition, key.derived.execution} {
						s, err := (boundTree{expression: node}).StructuralOutput(ctx)
						if err != nil {
							t.Fatal(err)
						}
						result = append(result, s)
					}
				}
				return result
			}
			originalTrees := snapshot(cover)
			originalTuples := [][]string{{"a", "a"}, {"a ", "a "}}
			if got := expressionRootTuples(t, coverRoot); !reflect.DeepEqual(got, originalTuples) {
				t.Fatal("actual derived root lost independent padding/TEXT tuple order", got, originalTuples)
			}
			compacted := *parent
			compacted.attributes = append([]int32(nil), parent.attributes[1:]...)
			compacted.columns = append([]ColumnDescriptor(nil), parent.columns[1:]...)
			compacted.index = map[string]int{"n": 0, "s": 1, "p": 2}
			layout := e.BeginCatalogStage(view)
			if err := layout.Replace(&compacted); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(layout, ids.ObjectAddress{}, nil); err != nil {
				t.Fatal(err)
			}
			compactedView := e.stageRead(layout)
			compactedCover := compactedView.MustRelation(cover.address)
			compactedUnsigned := compactedView.MustRelation(unsigned.address)
			compactedStore := &relationStorage{}
			for _, row := range store.versions {
				row.values = append([]sqltype.Value(nil), row.values[1:]...)
				compactedStore.versions = append(compactedStore.versions, row)
			}
			if got := expressionRootTuples(t, root(&compacted, compactedCover, compactedStore)); !reflect.DeepEqual(got, originalTuples) {
				t.Fatal("DROP compaction lost derived frames", got, originalTuples)
			}
			if compactedCover.sqlIndex.keys[0].derived == cover.sqlIndex.keys[0].derived || !reflect.DeepEqual(compactedCover.sqlIndex.keys[0].derived.attributes, compacted.attributes) {
				t.Fatal("DROP did not detach/remap derived trees")
			}
			compactedTrees := snapshot(compactedCover)
			final := compacted
			final.columns = append([]ColumnDescriptor(nil), compacted.columns...)
			query, attribute := "ALTER TABLE t ALTER p TYPE TEXT", int32(4)
			if dependencyType {
				query, attribute = "ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", 3
				final.columns[1].Usage = varchar
			}
			action := alterAddUnitAction(t, query)
			queue, err := captureAlterTypeIndexes(scope, compactedView, &compacted, &compacted, []parser.AlterTableAction{action})
			if err != nil {
				t.Fatal(err)
			}
			if dependencyType && (len(queue) != 1 || !strings.Contains(queue[0].sql, "bpchar_ops") || !strings.Contains(queue[0].sql, "INCLUDE (p)")) || !dependencyType && len(queue) != 0 {
				t.Fatal("TYPE did not capture the decorated original definition once", queue)
			}
			stage := e.BeginCatalogStage(compactedView)
			if err := stage.Replace(&final); err != nil {
				t.Fatal(err)
			}
			auth.catalog = compactedView
			auth.recaptureCatalog(auth.catalog)
			if err := e.rebindAlterTypeIndexes(scope, auth, &final, stage, queue); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(stage, parent.address, []columnTransformation{{attribute: attribute}}); err != nil {
				t.Fatal(err)
			}
			finalView := e.stageRead(stage)
			next := finalView.MustRelation(cover.address)
			if next == compactedCover || next.sqlIndex == compactedCover.sqlIndex || next.sqlIndex.keys[1].explicitClass || next.sqlIndex.keys[1].collation != 0 || !next.sqlIndex.keys[0].explicitClass || next.sqlIndex.keys[0].class.Name != "bpchar_ops" || next.sqlIndex.keys[0].collation != 951 || !next.sqlIndex.keys[0].desc || next.sqlIndex.keys[0].nullsFirst || !reflect.DeepEqual(next.sqlIndex.include, []int32{4}) {
				t.Fatal("TYPE lost providers/options/INCLUDE or old-default normalization", next.sqlIndex)
			}
			for i, key := range next.sqlIndex.keys {
				if key.derived == nil || key.derived.execution.ResultUsage() != text || !reflect.DeepEqual(key.derived.attributes, final.attributes) || len(key.derived.dependencies) == 0 {
					t.Fatal("TYPE lost derived Usage/layout/dependencies", key)
				}
				if dependencyType == (key.derived == compactedCover.sqlIndex.keys[i].derived) {
					t.Fatal("TYPE failed to preserve/rebind the correct derived owner")
				}
				dependent := false
				for _, dependency := range key.derived.dependencies {
					if dependency.Address == (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 3}) && dependency.Domain() == e.catalog.domain && dependency.Epoch == view.Epoch() {
						dependent = true
					}
				}
				if !dependent {
					t.Fatal("TYPE lost actual stable source dependency", key.derived.dependencies)
				}
			}
			if err := validateIndexDefinition(finalView, next); err != nil {
				t.Fatal(err)
			}
			finalStore := &relationStorage{}
			for _, row := range compactedStore.versions {
				row.values = append([]sqltype.Value(nil), row.values...)
				if dependencyType {
					s, err := row.values[1].Text()
					if err != nil {
						t.Fatal(err)
					}
					row.values[1] = sqltype.String(varchar.Type, s)
				}
				finalStore.versions = append(finalStore.versions, row)
			}
			if got := expressionRootTuples(t, root(&final, next, finalStore)); !reflect.DeepEqual(got, originalTuples) {
				t.Fatal("TYPE lost derived root values", got, originalTuples)
			}
			nextUnsigned := finalView.MustRelation(unsigned.address)
			if got := root(&final, nextUnsigned, finalStore); got.entries[0].row != 2 || nextUnsigned.sqlIndex.keys[0].class.Name != "oid_ops" || !nextUnsigned.sqlIndex.keys[0].explicitClass {
				t.Fatal("TYPE lost actual unsigned derived root", got)
			}
			if !reflect.DeepEqual(snapshot(cover), originalTrees) || !reflect.DeepEqual(snapshot(compactedCover), compactedTrees) || !reflect.DeepEqual(expressionRootTuples(t, coverRoot), originalTuples) || parent.columns[2].Usage != text || compacted.columns[1].Usage != text || compactedUnsigned.sqlIndex.keys[0].derived.execution.ResultUsage() != integer {
				t.Fatal("TYPE/remap mutated retained predecessor trees/roots/Usage")
			}
		})
	}
}
