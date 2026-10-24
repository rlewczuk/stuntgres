package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
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

func indexClausesFixture(t *testing.T) (*catalog.Relation, *catalog.Relation, *catalog.State, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	types := indexClausesTypes()
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20, 30}, ColumnMeta: catalog.ColumnMetadata{HighWater: 30}, Columns: []catalog.Column{{Name: "a", Usage: types[sqltype.Int4].Usage()}, {Name: "b", Usage: types[sqltype.Int4].Usage()}, {Name: "p", Usage: types[sqltype.Text].Usage()}}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	c, ok := operatorFamilyTestRead().DefaultIndexClass(403, types[sqltype.Int4])
	if !ok {
		t.Fatal("missing current integer default class")
	}
	def := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 10, Class: c}, {Attribute: 20, Class: c, Desc: true, NullsFirst: true}}, Include: []int32{30}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "cover", ObjectRevision: 1, RelationKind: catalog.IndexRelation, SQLIndex: def, Columns: append([]catalog.Column(nil), parent.Columns...), Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	catalogSnapshot := operatorFamilyFixtureState(catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, idx.Address(): idx}, nil))
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	return parent, idx, catalogSnapshot, types
}

func TestIndexClausesRootAttributes(t *testing.T) {
	parent, idx, _, types := indexClausesFixture(t)
	ctx := context.Background()
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(types[sqltype.Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	store := &storage.Relation{Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{integer(1), integer(2), sqltype.String(types[sqltype.Text], "x")}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{integer(1), sqltype.Null(types[sqltype.Int4]), sqltype.String(types[sqltype.Text], "z")}},
		{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{integer(1), integer(2), sqltype.Null(types[sqltype.Text])}},
	}}
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 3 || root.Entries[0].Row != 2 || root.Entries[1].Row != 1 || root.Entries[2].Row != 3 {
		t.Fatal(root)
	}
	for _, entry := range root.Entries {
		if len(entry.Keys) != 2 {
			t.Fatal("INCLUDE entered logical key", entry)
		}
	}
	ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, idx.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := ordering.CompareKeys(ctx, root.Entries[1], root.Entries[2]); err != nil || cmp != 0 {
		t.Fatal("payload affected equality", cmp, err)
	}
	successor := store.Versions[0]
	successor.Base = 1
	successor.Version = 4
	successor.Values = []sqltype.Value{integer(1), integer(2), sqltype.String(types[sqltype.Text], "changed")}
	merged, err := index.MergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx, root.Entries, &index.RowPublication{Writes: []storage.RowVersion{successor}}, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Entries) != 3 || merged.Entries[1].Version != 4 || root.Entries[1].Version != 1 || len(merged.Entries[1].Keys) != 2 {
		t.Fatal("immutable payload-only merge", root, merged)
	}
	// Included NAME[] has storage but no ordering provider.
	domain, err := sqltype.Domain(21000, "payload", "public.payload", types[sqltype.NameArray].Usage())
	if err != nil {
		t.Fatal(err)
	}
	next := *parent
	next.Columns = append([]catalog.Column(nil), parent.Columns...)
	next.Columns[2].Usage = domain.Usage()
	evolved, err := index.EvolveIndex(operatorFamilyTestRead(), idx, parent, &next, []index.ColumnTransformation{{Attribute: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if evolved.Columns[2].Usage.Type != domain || len(evolved.SQLIndex.Keys) != 2 {
		t.Fatal(evolved)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ordering.CompareKeys(canceled, root.Entries[1], root.Entries[2]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: canceled}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestIndexClausesClassRoots(t *testing.T) {
	parent, idx, catalogSnapshot, types := indexClausesFixture(t)
	ctx := context.Background()
	oid, _ := operatorFamilyTestRead().IndexClass(1981)
	idx.SQLIndex = &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 10, Class: oid, ExplicitClass: true}}, Include: []int32{30}, Ready: true, Valid: true, Live: true}
	idx.Columns = []catalog.Column{parent.Columns[0], parent.Columns[2]}
	idx.Attributes = []int32{1, 2}
	idx.ColumnMeta.HighWater = 2
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	negative, _ := sqltype.Integer(types[sqltype.Int4], -1)
	store := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{negative, one, sqltype.Null(types[sqltype.Text])}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "p")}}}}
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.Entries[0].Row != 2 || root.Entries[1].Row != 1 {
		t.Fatal("oid_ops did not choose unsigned provider", root)
	}
	bpchar, _ := operatorFamilyTestRead().IndexClass(10004)
	def := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 30, Class: bpchar, ExplicitClass: true, Collation: 950}}, Ready: true, Valid: true, Live: true}
	ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, def)
	if err != nil {
		t.Fatal(err)
	}
	a := storage.IndexEntry{Keys: []sqltype.Value{sqltype.String(types[sqltype.Text], "a")}}
	b := storage.IndexEntry{Keys: []sqltype.Value{sqltype.String(types[sqltype.Text], "a ")}}
	if cmp, err := ordering.CompareKeys(ctx, a, b); err != nil || cmp != 0 {
		t.Fatal("bpchar class lost padding semantics", cmp, err)
	}
}

func TestIndexClausesUniquePayload(t *testing.T) {
	parent, idx, catalogSnapshot, types := indexClausesFixture(t)
	ctx := context.Background()
	c := catalog.UniqueConstraint{OID: 22000, Name: "cover", Attributes: []int32{10}, Index: idx.Address()}
	parent.Constraints.Unique = []catalog.UniqueConstraint{c}
	class, _ := catalog.NewRead(nil, catalogSnapshot).DefaultIndexClass(403, types[sqltype.Int4])
	idx.SQLIndex = &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 10, Class: class}}, Include: []int32{30}, Unique: true, NullsNotDistinct: c.NullsNotDistinct, Constraint: index.UniqueBacking(&c), Ready: true, Valid: true, Live: true}
	idx.Columns = []catalog.Column{parent.Columns[0], parent.Columns[2]}
	idx.Attributes = []int32{1, 2}
	idx.ColumnMeta.HighWater = 2
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	if err := index.ValidateUniques(catalog.NewRead(nil, catalogSnapshot), parent, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	for _, payload := range []sqltype.Value{sqltype.String(types[sqltype.Text], "different"), sqltype.Null(types[sqltype.Text])} {
		store := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "first")}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one, one, payload}}}}
		root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(root.Entries) != 2 || len(root.Entries[0].Keys) != 1 {
			t.Fatal(root)
		}
		err = index.ValidateUniqueRoot(ctx, parent, idx, root, "public", sqltype.OutputEnvironment{}, &access.View{Catalog: operatorFamilyTestRead()}, nil, false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Detail != "Key (a)=(1) already exists." {
			t.Fatal("INCLUDE weakened actual unique root", err)
		}
	}
}

func TestIndexClausesEvolution(t *testing.T) {
	parent, idx, catalogSnapshot, types := indexClausesFixture(t)
	idx.SQLIndex.Keys[0].ExplicitClass = true
	textPattern, _ := catalog.NewRead(nil, catalogSnapshot).IndexClass(4217)
	idx.SQLIndex.Keys[1] = catalog.IndexKey{Attribute: 30, Class: textPattern, ExplicitClass: true, Collation: 951}
	idx.SQLIndex.Include = []int32{20}
	idx.Columns = []catalog.Column{parent.Columns[0], parent.Columns[2], parent.Columns[1]}
	idx.Columns[1].Usage = index.KeyCollation(parent.Columns[2].Usage, idx.SQLIndex.Keys[1])
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	// Included TYPE normalizes every OLD default, not only the changed key.
	next := *parent
	next.Columns = append([]catalog.Column(nil), parent.Columns...)
	next.Columns[1].Usage = types[sqltype.Text].Usage()
	evolved, err := index.EvolveIndex(operatorFamilyTestRead(), idx, parent, &next, []index.ColumnTransformation{{Attribute: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if evolved.SQLIndex == idx.SQLIndex || evolved.SQLIndex.Keys[0].ExplicitClass || !idx.SQLIndex.Keys[0].ExplicitClass || evolved.SQLIndex.Keys[1].Class != textPattern || evolved.SQLIndex.Keys[1].Collation != 951 {
		t.Fatal("whole-index reconstruction/source immutability", idx, evolved)
	}
	newer := next
	newer.Columns = append([]catalog.Column(nil), next.Columns...)
	newer.Columns[0].Usage = types[sqltype.Text].Usage()
	rebuilt, err := index.EvolveIndex(operatorFamilyTestRead(), evolved, &next, &newer, []index.ColumnTransformation{{Attribute: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.SQLIndex.Keys[0].Class.Name != "text_ops" || rebuilt.Columns[0].Usage.Type != types[sqltype.Text] {
		t.Fatal(rebuilt)
	}
	oid, _ := catalog.NewRead(nil, catalogSnapshot).IndexClass(1981)
	retained := *idx
	def := *idx.SQLIndex
	def.Keys = append([]catalog.IndexKey(nil), def.Keys...)
	def.Keys[0].Class = oid
	retained.SQLIndex = &def
	_, err = index.EvolveIndex(operatorFamilyTestRead(), &retained, parent, &newer, []index.ColumnTransformation{{Attribute: 10}})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42804" || retained.SQLIndex.Keys[0].Class != oid {
		t.Fatal("nondefault class was replaced", err)
	}
	dropped := *parent
	dropped.Attributes = []int32{10, 30}
	dropped.Columns = []catalog.Column{parent.Columns[0], parent.Columns[2]}
	if removed, err := index.EvolveIndex(operatorFamilyTestRead(), idx, parent, &dropped, nil); err != nil || removed != nil {
		t.Fatal("included DROP retained dependency", removed, err)
	}
	moved := *parent
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 23000}
	movedIndex, err := index.EvolveIndex(operatorFamilyTestRead(), idx, parent, &moved, nil)
	if err != nil || movedIndex.SQLIndex != idx.SQLIndex {
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
			parent, idx, catalogSnapshot, types := indexClausesFixture(t)
			parent.Columns[1].Usage = types[sqltype.Text].Usage()
			textClass, _ := catalog.NewRead(nil, catalogSnapshot).DefaultIndexClass(403, types[sqltype.Text])
			idx.SQLIndex.Keys[0].ExplicitClass = true
			idx.SQLIndex.Keys[1].Class = textClass
			idx.SQLIndex.Keys[1].ExplicitClass = true
			idx.SQLIndex.Keys[1].Collation = parent.Columns[1].Usage.Collation.ID
			idx.Columns[1].Usage = index.KeyCollation(parent.Columns[1].Usage, idx.SQLIndex.Keys[1])
			if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
				t.Fatal(err)
			}
			definition := idx.SQLIndex
			keys := append([]catalog.IndexKey(nil), definition.Keys...)
			columns := append([]catalog.Column(nil), idx.Columns...)
			parentColumns := append([]catalog.Column(nil), parent.Columns...)
			e := New()
			stage := e.BeginCatalogStage(e.catalog.Read(catalogSnapshot))
			if err := stage.Replace(parent); err != nil {
				t.Fatal(err)
			}
			transforms := []index.ColumnTransformation{{Attribute: tc.attribute, Rewrite: tc.rewrite}}
			if err := e.evolveStageIndexesLocked(stage, parent.Address(), transforms); err != nil {
				t.Fatal(err)
			}
			evolved, ok := stage.Get(idx.Address()).(*catalog.Relation)
			if !ok || evolved == idx || evolved.SQLIndex == definition || evolved.Revision() != idx.Revision()+1 {
				t.Fatal("same-Usage TYPE did not detach dependent definition", evolved)
			}
			for i, key := range evolved.SQLIndex.Keys {
				if key.ExplicitClass || key.Collation != 0 || key.Class != keys[i].Class || key.Desc != keys[i].Desc || key.NullsFirst != keys[i].NullsFirst {
					t.Fatal("whole-index default normalization lost key semantics", key)
				}
			}
			if idx.SQLIndex != definition || !reflect.DeepEqual(definition.Keys, keys) || !reflect.DeepEqual(idx.Columns, columns) || !reflect.DeepEqual(parent.Columns, parentColumns) {
				t.Fatal("TYPE evolution mutated original descriptor")
			}
			if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), evolved); err != nil {
				t.Fatal(err)
			}
			for _, changes := range [][]index.ColumnTransformation{nil, {{Attribute: 99}}} {
				unchanged, err := index.EvolveIndex(operatorFamilyTestRead(), idx, parent, parent, changes)
				if err != nil || unchanged != idx {
					t.Fatal("nondependent operation normalized defaults", unchanged, err)
				}
			}
			other := parent.Address()
			other.ObjectID += 2
			unrelated := e.BeginCatalogStage(e.catalog.Read(catalogSnapshot))
			if err := unrelated.Replace(parent); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(unrelated, other, transforms); err != nil || unrelated.Get(idx.Address()) != nil {
				t.Fatal("TYPE attribute leaked across parent identity", err)
			}
		})
	}
}

func TestIndexClausesUntouchedStage(t *testing.T) {
	parent, idx, catalog, _ := indexClausesFixture(t)
	e := New()
	stage := e.BeginCatalogStage(e.catalog.Read(catalog))
	transforms := []index.ColumnTransformation{{Attribute: parent.Attributes[0]}}
	var err error
	allocations := testing.AllocsPerRun(100, func() {
		err = e.evolveStageIndexesLocked(stage, parent.Address(), transforms)
	})
	if err != nil || allocations != 0 || stage.DescriptorCount() != 0 || catalog.Get(idx.Address()) != idx {
		t.Fatal("untouched parent evolution allocated or changed descriptors", allocations, err)
	}
}

func TestIndexClausesReplacement(t *testing.T) {
	parent, idx, catalog, types := indexClausesFixture(t)
	ctx := context.Background()
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	source := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 2, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}
	original, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := &storage.Relation{Versions: append([]storage.RowVersion(nil), source.Versions...)}
	e := New()
	e.storage.SetFrontierLocked(100, 200)
	e.catalog.PublishLocked(catalog)
	e.mu.Lock()
	e.storage.InstallRelationLocked(parent.Address(), source)
	e.mu.Unlock()
	e.mu.Lock()
	e.storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original})
	e.mu.Unlock()
	e.visibility.SetCommitSeqLocked(1)
	snapshot, err := e.captureIndexSnapshot(ctx, e.catalog.Read(catalog), map[ids.ObjectAddress]*storage.Relation{parent.Address(): source}, map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original}, 1)
	if err != nil {
		t.Fatal(err)
	}
	roots, rows, versions, err := e.prepareRemintedStorage(ctx, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 101 || versions != 201 || roots[idx.Address()].Entries[0].Row != 101 || len(roots[idx.Address()].Entries[0].Keys) != 2 || e.storage.RowFrontier() != 100 || e.storage.VersionFrontier() != 200 {
		t.Fatal("replacement did not privately remint actual key-only root", roots, rows, versions)
	}
	if !reflect.DeepEqual(idx.SQLIndex.Include, []int32{30}) || source.Versions[0].Row != 1 || original.Entries[0].Version != 2 {
		t.Fatal("replacement mutated source")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := e.prepareRemintedStorage(canceled, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.storage.RowFrontier() != 100 || e.storage.VersionFrontier() != 200 {
		t.Fatal("failed replacement published allocator")
	}
}

func TestIndexClausesValidationOnlyReplacement(t *testing.T) {
	parent, index, snapshot, types := indexClausesFixture(t)
	one, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {Versions: []storage.RowVersion{{Row: 1, Version: 2, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}}
	e := New()
	// This is a deliberately reduced but valid key-construction catalogue:
	// namespace descriptors are needed for UNIQUE diagnostics, not nonunique
	// ordering/provider/shape validation or replacement materialization.
	roots, err := e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, snapshot, relations, nil, nil, nil, nil, false, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots != nil {
		t.Fatal("validation-only replacement required unrelated namespace metadata or materialized roots", roots, err)
	}
	broken := *index
	definition := *index.SQLIndex
	definition.Keys = append([]catalog.IndexKey(nil), index.SQLIndex.Keys...)
	definition.Keys[0].Attribute = 999
	broken.SQLIndex = &definition
	invalid := catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, index.Address(): &broken}, nil)
	for _, materialize := range []bool{false, true} {
		if _, err := e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, invalid, relations, nil, nil, nil, nil, materialize, sqltype.OutputEnvironment{}, nil, nil, nil); err == nil {
			t.Fatal("phase switch weakened actual index definition validation", materialize)
		}
	}
}

func TestIndexClausesDerivedProviders(t *testing.T) {
	parent, _, _, types := indexClausesFixture(t)
	typ := types[sqltype.Text]
	usage := typ.Usage()
	column := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 2}
	plan, err := sqltype.BindString("lower", usage, usage, []sqltype.Usage{usage}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	node := functions.String{Info: column.Info, Plan: plan, Args: []expr.Expr{column}, Textual: [4]bool{true}}
	class, _ := operatorFamilyTestRead().IndexClass(10004)
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: node, Execution: node, Attributes: parent.Attributes}, Class: class, ExplicitClass: true, Collation: 950}}, Include: []int32{10}, Ready: true, Valid: true, Live: true}
	store := &storage.Relation{}
	for i, text := range []string{"Alpha ", "ALPHA"} {
		payload, err := sqltype.Integer(types[sqltype.Int4], int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Values: []sqltype.Value{payload, payload, sqltype.String(typ, text)}})
	}
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 2 || root.Entries[0].Row != 1 || root.Entries[1].Row != 2 {
		t.Fatal("derived key used default TEXT ordering instead of padding semantics", root)
	}
	ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := ordering.CompareKeys(context.Background(), root.Entries[0], root.Entries[1]); err != nil || cmp != 0 {
		t.Fatal("derived explicit class did not own equality", cmp, err)
	}
	effective, err := index.KeyUsage(parent, definition.Keys[0])
	if err != nil || effective.Type != typ || effective.Collation.ID != 950 || parent.Columns[2].Usage != usage {
		t.Fatal("derived effective Usage mutated result or parent identity", effective, err)
	}
	for i, want := range []string{"alpha ", "alpha"} {
		entry := root.Entries[i]
		text, err := entry.Keys[0].Text()
		if err != nil || len(entry.Keys) != 1 || entry.Keys[0].Type() != typ || text != want {
			t.Fatal("derived comparison rewrote value typing or copied INCLUDE payload", entry, err)
		}
	}
	text, err := store.Versions[0].Values[2].Text()
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
			parent.RelationKind = catalog.TableRelation
			parent.Attributes = []int32{1, 2, 3, 4}
			parent.ColumnMeta.HighWater = 4
			parent.Columns = []catalog.Column{{Name: "extra", Usage: integer}, {Name: "n", Usage: integer}, {Name: "s", Usage: text}, {Name: "p", Usage: text}}
			parent.Index = map[string]int{"extra": 0, "n": 1, "s": 2, "p": 3}
			for i := range parent.Columns {
				parent.Columns[i].Nullable, parent.Columns[i].NullabilityKnown = true, true
				parent.Columns[i].OriginTable, parent.Columns[i].OriginAttribute = 20000, int16(i+1)
			}
			setup := e.BeginCatalogStage(auth.Catalog)
			if err := setup.Create(parent); err != nil {
				t.Fatal(err)
			}
			auth.Catalog = e.stageRead(setup)
			auth.Recapture(auth.Catalog)
			bind := func(name, query string, oid ids.OID) *catalog.Relation {
				raw, err := parser.Parse(query)
				if err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				definition, columns, attributes, labels, err := index.BindAttributes(ddlHost{e: e, op: scope.op}, auth, scope.input, parent, raw[0].(*parser.CreateIndex))
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), Owner: parent.Owner, ObjectName: name, ObjectRevision: 1, RelationKind: catalog.IndexRelation, SQLIndex: definition, Columns: columns, Attributes: attributes, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(columns))}, Index: labels}
				if err := setup.Create(index); err != nil {
					t.Fatal(err)
				}
				return index
			}
			unsigned := bind("unsigned", "CREATE INDEX unsigned ON t((n+0) oid_ops) INCLUDE(p)", 20001)
			cover := bind("cover", `CREATE INDEX cover ON t(lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST,lower(s) COLLATE "default" text_ops) INCLUDE(p)`, 20002)
			view := e.stageRead(setup)
			auth.Catalog = view
			auth.Recapture(auth.Catalog)
			for _, idx := range []*catalog.Relation{unsigned, cover} {
				if err := index.ValidateIndexDefinition(view, idx); err != nil {
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
			store := &storage.Relation{Versions: []storage.RowVersion{
				{Row: 1, Version: 1, Values: []sqltype.Value{value(0), value(-1), sqltype.String(text.Type, "a "), sqltype.String(text.Type, "space")}},
				{Row: 2, Version: 2, Values: []sqltype.Value{value(0), value(1), sqltype.String(text.Type, "a"), sqltype.String(text.Type, "plain")}},
			}}
			root := func(owner, idx *catalog.Relation, stored *storage.Relation) *storage.IndexRoot {
				r, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, owner, idx.SQLIndex, stored, nil)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			unsignedRoot, coverRoot := root(parent, unsigned, store), root(parent, cover, store)
			if unsignedRoot.Entries[0].Row != 2 || unsignedRoot.Entries[1].Row != 1 || len(unsignedRoot.Entries[0].Keys) != 1 || unsignedRoot.Entries[0].Keys[0].Type() != integer.Type {
				t.Fatal("actual bound derived key lost unsigned semantics or original typing", unsignedRoot)
			}
			padding, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, cover.SQLIndex)
			if err != nil {
				t.Fatal(err)
			}
			if cmp, err := padding.Comparisons[0].Compare(ctx, coverRoot.Entries[0].Keys[0], coverRoot.Entries[1].Keys[0], scope.input); err != nil || cmp != 0 {
				t.Fatal("actual bound lower(TEXT) lost padding peers", cmp, err)
			}
			if cmp, err := padding.Comparisons[1].Compare(ctx, coverRoot.Entries[0].Keys[1], coverRoot.Entries[1].Keys[1], scope.input); err != nil || cmp >= 0 {
				t.Fatal("ordinary TEXT distinction was lost", cmp, err)
			}
			snapshot := func(index *catalog.Relation) []string {
				var result []string
				for _, key := range index.SQLIndex.Keys {
					for _, node := range []expr.Expr{key.Derived.Definition, key.Derived.Execution} {
						s, err := (plan.BoundTree{Expression: node}).StructuralOutput(ctx)
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
			compacted.Attributes = append([]int32(nil), parent.Attributes[1:]...)
			compacted.Columns = append([]catalog.Column(nil), parent.Columns[1:]...)
			compacted.Index = map[string]int{"n": 0, "s": 1, "p": 2}
			layout := e.BeginCatalogStage(view)
			if err := layout.Replace(&compacted); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(layout, ids.ObjectAddress{}, nil); err != nil {
				t.Fatal(err)
			}
			compactedView := e.stageRead(layout)
			compactedCover := compactedView.MustRelation(cover.Address())
			compactedUnsigned := compactedView.MustRelation(unsigned.Address())
			compactedStore := &storage.Relation{}
			for _, row := range store.Versions {
				row.Values = append([]sqltype.Value(nil), row.Values[1:]...)
				compactedStore.Versions = append(compactedStore.Versions, row)
			}
			if got := expressionRootTuples(t, root(&compacted, compactedCover, compactedStore)); !reflect.DeepEqual(got, originalTuples) {
				t.Fatal("DROP compaction lost derived frames", got, originalTuples)
			}
			if compactedCover.SQLIndex.Keys[0].Derived == cover.SQLIndex.Keys[0].Derived || !reflect.DeepEqual(compactedCover.SQLIndex.Keys[0].Derived.Attributes, compacted.Attributes) {
				t.Fatal("DROP did not detach/remap derived trees")
			}
			compactedTrees := snapshot(compactedCover)
			final := compacted
			final.Columns = append([]catalog.Column(nil), compacted.Columns...)
			query, attribute := "ALTER TABLE t ALTER p TYPE TEXT", int32(4)
			if dependencyType {
				query, attribute = "ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", 3
				final.Columns[1].Usage = varchar
			}
			action := alterAddUnitAction(t, query)
			queue, err := captureAlterTypeIndexes(scope, compactedView, &compacted, &compacted, []parser.AlterTableAction{action})
			if err != nil {
				t.Fatal(err)
			}
			if dependencyType && (len(queue) != 1 || !strings.Contains(queue[0].SQL, "bpchar_ops") || !strings.Contains(queue[0].SQL, "INCLUDE (p)")) || !dependencyType && len(queue) != 0 {
				t.Fatal("TYPE did not capture the decorated original definition once", queue)
			}
			stage := e.BeginCatalogStage(compactedView)
			if err := stage.Replace(&final); err != nil {
				t.Fatal(err)
			}
			auth.Catalog = compactedView
			auth.Recapture(auth.Catalog)
			if err := e.rebindAlterTypeIndexes(scope, auth, &final, stage, queue); err != nil {
				t.Fatal(err)
			}
			if err := e.evolveStageIndexesLocked(stage, parent.Address(), []index.ColumnTransformation{{Attribute: attribute}}); err != nil {
				t.Fatal(err)
			}
			finalView := e.stageRead(stage)
			next := finalView.MustRelation(cover.Address())
			if next == compactedCover || next.SQLIndex == compactedCover.SQLIndex || next.SQLIndex.Keys[1].ExplicitClass || next.SQLIndex.Keys[1].Collation != 0 || !next.SQLIndex.Keys[0].ExplicitClass || next.SQLIndex.Keys[0].Class.Name != "bpchar_ops" || next.SQLIndex.Keys[0].Collation != 951 || !next.SQLIndex.Keys[0].Desc || next.SQLIndex.Keys[0].NullsFirst || !reflect.DeepEqual(next.SQLIndex.Include, []int32{4}) {
				t.Fatal("TYPE lost providers/options/INCLUDE or old-default normalization", next.SQLIndex)
			}
			for i, key := range next.SQLIndex.Keys {
				if key.Derived == nil || key.Derived.Execution.ResultUsage() != text || !reflect.DeepEqual(key.Derived.Attributes, final.Attributes) || len(key.Derived.Dependencies) == 0 {
					t.Fatal("TYPE lost derived Usage/layout/dependencies", key)
				}
				if dependencyType == (key.Derived == compactedCover.SQLIndex.Keys[i].Derived) {
					t.Fatal("TYPE failed to preserve/rebind the correct derived owner")
				}
				dependent := false
				for _, dependency := range key.Derived.Dependencies {
					if dependency.Address == (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 3}) && dependency.Domain() == e.catalog.Domain && dependency.Epoch == view.Epoch() {
						dependent = true
					}
				}
				if !dependent {
					t.Fatal("TYPE lost actual stable source dependency", key.Derived.Dependencies)
				}
			}
			if err := index.ValidateIndexDefinition(finalView, next); err != nil {
				t.Fatal(err)
			}
			finalStore := &storage.Relation{}
			for _, row := range compactedStore.Versions {
				row.Values = append([]sqltype.Value(nil), row.Values...)
				if dependencyType {
					s, err := row.Values[1].Text()
					if err != nil {
						t.Fatal(err)
					}
					row.Values[1] = sqltype.String(varchar.Type, s)
				}
				finalStore.Versions = append(finalStore.Versions, row)
			}
			if got := expressionRootTuples(t, root(&final, next, finalStore)); !reflect.DeepEqual(got, originalTuples) {
				t.Fatal("TYPE lost derived root values", got, originalTuples)
			}
			nextUnsigned := finalView.MustRelation(unsigned.Address())
			if got := root(&final, nextUnsigned, finalStore); got.Entries[0].Row != 2 || nextUnsigned.SQLIndex.Keys[0].Class.Name != "oid_ops" || !nextUnsigned.SQLIndex.Keys[0].ExplicitClass {
				t.Fatal("TYPE lost actual unsigned derived root", got)
			}
			if !reflect.DeepEqual(snapshot(cover), originalTrees) || !reflect.DeepEqual(snapshot(compactedCover), compactedTrees) || !reflect.DeepEqual(expressionRootTuples(t, coverRoot), originalTuples) || parent.Columns[2].Usage != text || compacted.Columns[1].Usage != text || compactedUnsigned.SQLIndex.Keys[0].Derived.Execution.ResultUsage() != integer {
				t.Fatal("TYPE/remap mutated retained predecessor trees/roots/Usage")
			}
		})
	}
}
