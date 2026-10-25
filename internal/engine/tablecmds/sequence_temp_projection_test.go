package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Retain the published maps, not merely a catalog.Read pointing at mutable maps.
type temporaryProjectionCatalog struct {
	view        catalog.Read
	descriptors map[ids.ObjectAddress]catalog.Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
}

func captureTemporaryProjectionCatalog(e *engine.Engine) temporaryProjectionCatalog {
	p := temporaryProjectionCatalog{view: e.CatalogRead(), descriptors: make(map[ids.ObjectAddress]catalog.Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress)}
	for a, d := range p.view.Descriptors() {
		p.descriptors[a] = d
	}
	for k, a := range p.view.Names() {
		p.names[k] = a
	}
	return p
}

func (p temporaryProjectionCatalog) unchanged(t *testing.T) {
	t.Helper()
	if !reflect.DeepEqual(maps.Collect(p.view.State().Descriptors()), p.descriptors) || !reflect.DeepEqual(maps.Collect(p.view.State().Names()), p.names) {
		t.Fatal("replacement/retirement mutated a retained catalog")
	}
}

func assertTemporaryProjectionProviders(t *testing.T, e *engine.Engine) {
	t.Helper()
	view := e.CatalogRead()
	for a, d := range view.Descriptors() {
		if err := e.Catalog.Providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal("invalid published descriptor", a, err)
		}
		found := view.Lookup(catalog.DescriptorNameKey(d))
		if len(found) != 1 || found[0] != a {
			t.Fatal("descriptor lost its unique name binding", a, found)
		}
		if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation && e.Storage.Sequences[a] == nil {
			t.Fatal("published sequence has no generator", a)
		}
	}
	for a := range e.Storage.Sequences {
		r, ok := view.Relation(a)
		if !ok || r.RelationKind != catalog.SequenceRelation {
			t.Fatal("orphan generator", a)
		}
	}
}

func endTemporaryProjectionSession(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func assertTemporaryProjectionTypedRows(t *testing.T, got [][]driver.Value) {
	t.Helper()
	want := [][]driver.Value{
		{int64(9223372036854775807), true, nil, nil},
		{int64(-9223372036854775808), false, nil, nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("type-generation transfer changed scalar payloads", got, want)
	}
}

func TestSequenceTemporarySelfReplacement(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE public.anchor(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO public.anchor VALUES(77)")
	enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE local_s CACHE 10")
	ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('local_s')")
	address := description.Sequences.Constants[0].Ref.Address
	namespace, token := s.TemporaryNamespace, s.SessionToken
	descriptor, nsDescriptor := e.Catalog.State.Get(address), e.Catalog.State.Get(namespace)
	held := func(want int64) {
		t.Helper()
		r, err := sequencePreparedExecute(e, s, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		got := enginetest.Tuples(r)
		r.Rows.Close()
		if len(got) != 1 || got[0][0] != want {
			t.Fatal("retained analysis lost cached progression", got, want)
		}
	}
	held(1)
	generator, memory := e.Storage.Sequences[address], engine.PayloadOf(s).Sequences.Memory(address)
	generatorState, memoryState := *generator, *memory
	before := captureTemporaryProjectionCatalog(e)
	sequenceRows := enginetest.SessionSQL(t, s, "SELECT last_value,is_called FROM local_s")
	tableRows := enginetest.SessionSQL(t, s, "SELECT v FROM public.anchor")
	for _, want := range []int64{2, 3} {
		if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
			t.Fatal("repeated self replacement", err)
		}
		if s.SessionToken != token || s.TemporaryNamespace != namespace || e.Catalog.State.Get(address) != descriptor || e.Catalog.State.Get(namespace) != nsDescriptor || e.Storage.Sequences[address] != generator || engine.PayloadOf(s).Sequences.Memory(address) != memory || *generator != generatorState || *memory != memoryState {
			t.Fatal("self replacement changed exact live TEMP authority/cache")
		}
		if e.Catalog.State.DescriptorCount() != len(before.descriptors) || e.Catalog.State.NameCount() != len(before.names) || len(e.Storage.Sequences) != 1 {
			t.Fatal("self replacement duplicated TEMP identities", e.Catalog.State.DescriptorCount(), e.Catalog.State.NameCount(), len(e.Storage.Sequences))
		}
		assertTemporaryProjectionProviders(t, e)
		before.unchanged(t)
		held(want)
		memoryState = *memory
	}
	if got := enginetest.Tuples(sequenceRows); len(got) != 1 || got[0][0] != int64(10) || got[0][1] != true {
		t.Fatal("retained sequence rows changed", got)
	}
	if got := enginetest.Tuples(tableRows); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("retained table rows changed", got)
	}
	endTemporaryProjectionSession(t, s)
	if e.Catalog.State.Get(address) != nil || e.Storage.Sequences[address] != nil {
		t.Fatal("physical end leaked self TEMP children")
	}
	assertTemporaryNamespacePair(t, e, s.SessionToken, namespace)
	before.unchanged(t)
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT v FROM public.anchor")); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("TEMP retirement removed persistent rows", got)
	}
	assertTemporaryProjectionProviders(t, e)
}

func TestSequenceTemporaryForeignReplacement(t *testing.T) {
	source, dest := engine.New(), engine.New()
	a, b := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, dest, nil)
	// The source's third user object is a persistent table; destination's
	// third object follows the real table/toast namespace pair and must collide.
	enginetest.SessionSQL(t, a, "CREATE SCHEMA collision_padding")
	for _, q := range []string{"CREATE SCHEMA portable", "CREATE TABLE portable.owner_table(v INT)", "INSERT INTO portable.owner_table VALUES(7)", "CREATE SEQUENCE portable.owned_s START 40 OWNED BY portable.owner_table.v", "CREATE TEMP SEQUENCE source_temp START 90 CACHE 10"} {
		enginetest.SessionSQL(t, a, q)
	}
	enginetest.SessionSQL(t, b, "CREATE TEMP SEQUENCE dest_temp CACHE 10")
	enginetest.SessionSQL(t, b, "CREATE TABLE public.old_table(v INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE portable.survivor(v INT,wide BIGINT,flag BOOL,absent_n BIGINT,absent_b BOOL)")
	enginetest.SessionSQL(t, a, "INSERT INTO portable.survivor VALUES(8,9223372036854775807,true,NULL,NULL),(9,-9223372036854775808,false,NULL,NULL)")
	enginetest.SessionSQL(t, b, "INSERT INTO public.old_table VALUES(77)")
	ast, description := sequencePreparedDescription(t, dest, b, "SELECT nextval('dest_temp')")
	address := description.Sequences.Constants[0].Ref.Address
	_, sourceDescription := sequencePreparedDescription(t, source, a, "SELECT nextval('source_temp')")
	sourceTemp := sourceDescription.Sequences.Constants[0].Ref.Address
	sourceTable := enginetest.RoleTableRef(t, source, "portable.owner_table").Address
	if sourceTable != address {
		t.Fatal("fixture did not exercise actual persistent/TEMP OID collision", sourceTable, address)
	}
	enginetest.SessionSQL(t, a, "CREATE INDEX owner_idx ON portable.owner_table(v)")
	sourceIndex, sourceIndexRoot := btreeRoot(t, source, "portable.owner_idx")
	sequenceScalar(t, a, "SELECT nextval('source_temp')", 90)
	sequenceScalar(t, b, "SELECT nextval('dest_temp')", 1)
	namespace, token := b.TemporaryNamespace, b.SessionToken
	sourceNamespace := a.TemporaryNamespace
	descriptor, nsDescriptor := dest.Catalog.State.Get(address), dest.Catalog.State.Get(namespace)
	generator, memory := dest.Storage.Sequences[address], engine.PayloadOf(b).Sequences.Memory(address)
	generatorState, memoryState := *generator, *memory
	sourceGenerator, sourceMemory := source.Storage.Sequences[sourceTemp], engine.PayloadOf(a).Sequences.Memory(sourceTemp)
	sourceGeneratorState, sourceMemoryState := *sourceGenerator, *sourceMemory
	sourceGenerators := make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	sourceStates := make(map[ids.ObjectAddress]storage.SequenceGenerator)
	for a, g := range source.Storage.Sequences {
		sourceGenerators[a], sourceStates[a] = g, *g
	}
	sourceBefore, destBefore := captureTemporaryProjectionCatalog(source), captureTemporaryProjectionCatalog(dest)
	sourceSurvivor := source.Catalog.State.MustRelation(enginetest.RoleTableRef(t, source, "portable.survivor").Address)
	sourceColumns := append([]catalog.Column(nil), sourceSurvivor.Columns...)
	sourceStore := source.Storage.Relations[sourceSurvivor.Address()]
	sourceValues := make([][]sqltype.Value, len(sourceStore.Versions))
	for i, row := range sourceStore.Versions {
		sourceValues[i] = append([]sqltype.Value(nil), row.Values...)
	}
	retainedTypedRows := enginetest.SessionSQL(t, a, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")
	retainedTypes := map[sqltype.ID]*sqltype.Descriptor{
		sqltype.Int8: descriptor.(*catalog.Relation).Columns[0].Usage.Type,
		sqltype.Bool: descriptor.(*catalog.Relation).Columns[2].Usage.Type,
	}
	oldRows := enginetest.SessionSQL(t, b, "SELECT v FROM public.old_table")
	sourceRows := enginetest.SessionSQL(t, a, "SELECT v FROM portable.owner_table")
	if _, err := dest.ReplaceCatalog(context.Background(), source, sourceBefore.view); err != nil {
		t.Fatal(err)
	}
	if b.SessionToken != token || b.TemporaryNamespace != namespace || dest.Catalog.State.Get(address) != descriptor || dest.Catalog.State.Get(namespace) != nsDescriptor || dest.Storage.Sequences[address] != generator || engine.PayloadOf(b).Sequences.Memory(address) != memory || *generator != generatorState || *memory != memoryState {
		t.Fatal("foreign replacement changed destination TEMP identity/cache")
	}
	var tempNamespaces, tempRelations int
	toast := assertTemporaryNamespacePair(t, dest, token, namespace)
	toastDescriptor := destBefore.view.Get(toast)
	if toastDescriptor == nil || dest.Catalog.State.Get(toast) != toastDescriptor {
		t.Fatal("foreign replacement changed destination toast identity")
	}
	for _, d := range dest.Catalog.State.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) {
			tempNamespaces++
			if ns.Temporary != b.SessionToken || (ns.Address() != namespace && ns.Address() != toast) || d != destBefore.view.Get(ns.Address()) {
				t.Fatal("imported foreign SessionToken", ns.Temporary)
			}
		}
		if d.Namespace() == namespace {
			tempRelations++
		}
		if d.Name() == "source_temp" {
			t.Fatal("imported source TEMP descriptor")
		}
	}
	if tempNamespaces != 2 || tempRelations != 1 || len(dest.Storage.Sequences) != 2 {
		t.Fatal("foreign replacement leaked TEMP descriptors/generators", tempNamespaces, tempRelations, len(dest.Storage.Sequences))
	}
	importedSurvivor := dest.Catalog.State.MustRelation(enginetest.RoleTableRef(t, dest, "portable.survivor").Address)
	for id, retainedType := range retainedTypes {
		actual, ok := dest.CatalogRead().LookupType(id)
		if !ok || actual != retainedType {
			t.Fatal("foreign replacement lost retained TEMP builtin dependency", id)
		}
	}
	for _, column := range importedSurvivor.Columns {
		actual, ok := dest.CatalogRead().LookupType(column.Usage.Type.ID())
		if !ok || actual != column.Usage.Type {
			t.Fatal("imported column uses a foreign type generation", column.Name)
		}
	}
	for _, row := range dest.Storage.Relations[importedSurvivor.Address()].Versions {
		for _, value := range row.Values {
			actual, ok := dest.CatalogRead().LookupType(value.Type().ID())
			if !ok || actual != value.Type() {
				t.Fatal("imported value uses a foreign type generation", value.Type().ID())
			}
		}
	}
	if !reflect.DeepEqual(sourceSurvivor.Columns, sourceColumns) {
		t.Fatal("type rebinding mutated source descriptor columns")
	}
	for i, column := range sourceSurvivor.Columns {
		if column.Usage.Type != sourceColumns[i].Usage.Type {
			t.Fatal("type rebinding changed source column type pointer")
		}
	}
	for i, row := range sourceStore.Versions {
		if !reflect.DeepEqual(row.Values, sourceValues[i]) {
			t.Fatal("type rebinding mutated source values")
		}
		for j, value := range row.Values {
			if value.Type() != sourceValues[i][j].Type() {
				t.Fatal("type rebinding changed source value type pointer")
			}
		}
	}
	assertTemporaryProjectionTypedRows(t, enginetest.Tuples(retainedTypedRows))
	assertTemporaryProjectionProviders(t, dest)
	sourceBefore.unchanged(t)
	for a, g := range sourceGenerators {
		if source.Storage.Sequences[a] != g || *g != sourceStates[a] {
			t.Fatal("foreign import changed source generator", a)
		}
	}
	destBefore.unchanged(t)
	if source.Catalog.State != sourceBefore.view.State() || source.Storage.Sequences[sourceTemp] != sourceGenerator || engine.PayloadOf(a).Sequences.Memory(sourceTemp) != sourceMemory || *sourceGenerator != sourceGeneratorState || *sourceMemory != sourceMemoryState {
		t.Fatal("destination import changed source live authority")
	}
	r, err := sequencePreparedExecute(dest, b, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	if got := enginetest.Tuples(r); len(got) != 1 || got[0][0] != int64(2) {
		t.Fatal("held destination cache did not survive foreign replacement", got)
	}
	r.Rows.Close()
	importedTable := enginetest.RoleTableRef(t, dest, "portable.owner_table").Address
	importedSequence := enginetest.RoleTableRef(t, dest, "portable.owned_s").Address
	if importedTable == sourceTable || dest.Catalog.State.MustRelation(importedSequence).Sequence.Owned != (ids.ObjectAddress{ClassID: importedTable.ClassID, ObjectID: importedTable.ObjectID, SubID: 1}) {
		t.Fatal("persistent collision did not remap OWNED BY", importedTable, dest.Catalog.State.Get(importedSequence))
	}
	sequenceScalar(t, b, "SELECT nextval('portable.owned_s')", 40)
	if got := enginetest.Tuples(enginetest.SessionSQL(t, b, "SELECT v FROM portable.owner_table")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("remapped persistent rows missing", got)
	}
	index, indexRoot := btreeRoot(t, dest, "portable.owner_idx")
	if index.SQLIndex.Parent != importedTable || sourceIndex.SQLIndex.Parent != sourceTable || index.SQLIndex == sourceIndex.SQLIndex {
		t.Fatal("index parent collision remapping lost source/destination authority")
	}
	if got := btreeKeys(t, indexRoot); !reflect.DeepEqual(got, []string{"7"}) {
		t.Fatal("reminted index membership", got)
	}
	enginetest.SessionSQL(t, b, "INSERT INTO portable.owner_table VALUES(8)")
	btreeWantKeys(t, dest, "portable.owner_idx", "7", "8")
	if got := btreeKeys(t, sourceIndexRoot); !reflect.DeepEqual(got, []string{"7"}) {
		t.Fatal("destination DML mutated source root", got)
	}
	if got := enginetest.Tuples(oldRows); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("retained destination rows changed", got)
	}
	endTemporaryProjectionSession(t, a)
	if source.Catalog.State.Get(sourceTemp) != nil || source.Storage.Sequences[sourceTemp] != nil {
		t.Fatal("source retirement leaked TEMP children")
	}
	assertTemporaryNamespacePair(t, source, a.SessionToken, sourceNamespace)
	sequenceScalar(t, b, "SELECT nextval('dest_temp')", 3)
	if dest.Storage.Sequences[address] != generator {
		t.Fatal("source retirement affected destination TEMP generator")
	}
	if got := enginetest.Tuples(sourceRows); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("source retirement changed retained rows", got)
	}
	enginetest.SessionSQL(t, b, "DROP TABLE portable.owner_table")
	if dest.Catalog.State.Get(importedTable) != nil || dest.Catalog.State.Get(importedSequence) != nil || dest.Storage.Sequences[importedSequence] != nil {
		t.Fatal("remapped ownership drop closure failed")
	}
	if dest.Catalog.State.Get(index.Address()) != nil || dest.Storage.IndexRoots[index.Address()] != nil {
		t.Fatal("remapped parent drop left index definition/root")
	}
	if source.Catalog.State.Get(sourceTable) == nil {
		t.Fatal("destination drop changed source persistent catalog")
	}
	endTemporaryProjectionSession(t, b)
	if dest.Catalog.State.Get(address) != nil || dest.Storage.Sequences[address] != nil {
		t.Fatal("destination retirement leaked TEMP")
	}
	assertTemporaryNamespacePair(t, dest, b.SessionToken, namespace)
	if got := enginetest.Tuples(enginetest.Run(t, dest, "SELECT v FROM portable.survivor ORDER BY v")); len(got) != 2 || got[0][0] != int64(8) || got[1][0] != int64(9) {
		t.Fatal("destination retirement removed persistent survivors", got)
	}
	assertTemporaryProjectionTypedRows(t, enginetest.Tuples(enginetest.Run(t, dest, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")))
	assertTemporaryProjectionTypedRows(t, enginetest.Tuples(enginetest.Run(t, source, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")))
	if got := enginetest.Tuples(enginetest.Run(t, source, "SELECT v FROM portable.owner_table")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("source retirement/drop removed persistent rows", got)
	}
	if got := enginetest.Tuples(enginetest.Run(t, source, "SELECT nextval('portable.owned_s')")); len(got) != 1 || got[0][0] != int64(40) {
		t.Fatal("foreign import/drop/retirement changed source persistent generator", got)
	}
	sourceBefore.unchanged(t)
	destBefore.unchanged(t)
	assertTemporaryProjectionProviders(t, source)
	assertTemporaryProjectionProviders(t, dest)
}

func TestSequenceTemporaryRetiredSourceReplacement(t *testing.T) {
	for _, self := range []bool{true, false} {
		t.Run(map[bool]string{true: "self", false: "foreign"}[self], func(t *testing.T) {
			source := engine.New()
			s := enginetest.StartSession(t, source, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE public.kept(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO public.kept VALUES(7)")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE public.kept_s START 40")
			enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE retired_temp START 90")
			_, description := sequencePreparedDescription(t, source, s, "SELECT nextval('retired_temp')")
			address, namespace := description.Sequences.Constants[0].Ref.Address, s.TemporaryNamespace
			captured := captureTemporaryProjectionCatalog(source)
			retained := enginetest.SessionSQL(t, s, "SELECT last_value FROM retired_temp")
			endTemporaryProjectionSession(t, s)
			if source.Storage.Sequences[address] != nil || source.Catalog.State.Get(address) != nil {
				t.Fatal("fixture TEMP generator/relation was not retired")
			}
			toast := assertTemporaryNamespacePair(t, source, s.SessionToken, namespace)
			tableDescriptor, toastDescriptor := source.Catalog.State.Get(namespace), source.Catalog.State.Get(toast)
			dest := source
			if !self {
				dest = engine.New()
			}
			if _, err := dest.ReplaceCatalog(context.Background(), source, captured.view); err != nil {
				t.Fatal("excluded retired TEMP required a deleted generator", err)
			}
			for _, d := range dest.Catalog.State.Descriptors() {
				if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) {
					if !self || (ns.Address() != namespace && ns.Address() != toast) {
						t.Fatal("imported foreign TEMP namespace")
					}
					if dest.Catalog.State.Get(ns.Address()) != source.Catalog.State.Get(ns.Address()) {
						t.Fatal("replaced retained empty namespace")
					}
				}
				if d.Name() == "retired_temp" {
					t.Fatal("resurrected retired TEMP relation")
				}
			}
			if self {
				if dest.Catalog.State.Get(namespace) != tableDescriptor || dest.Catalog.State.Get(toast) != toastDescriptor || s.TemporaryNamespace != (ids.ObjectAddress{}) {
					t.Fatal("self replacement changed retained pair/retired authority")
				}
			} else if dest.Catalog.State.Get(namespace) != nil || dest.Catalog.State.Get(toast) != nil {
				t.Fatal("foreign replacement imported retired source pair")
			}
			if len(dest.Storage.Sequences) != 1 {
				t.Fatal("retired TEMP generator imported", len(dest.Storage.Sequences))
			}
			if got := enginetest.Tuples(retained); len(got) != 1 || got[0][0] != int64(90) {
				t.Fatal("retained retired TEMP rows changed", got)
			}
			if got := enginetest.Tuples(enginetest.Run(t, dest, "SELECT v FROM public.kept")); len(got) != 1 || got[0][0] != int64(7) {
				t.Fatal("persistent source rows missing", got)
			}
			if got := enginetest.Tuples(enginetest.Run(t, dest, "SELECT nextval('public.kept_s')")); len(got) != 1 || got[0][0] != int64(40) {
				t.Fatal("persistent source generator missing", got)
			}
			captured.unchanged(t)
			assertTemporaryProjectionProviders(t, dest)
		})
	}
}

func btreeRoot(t *testing.T, e *engine.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	idx := e.Catalog.State.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}

func btreeKeys(t *testing.T, root *storage.IndexRoot) []string {
	t.Helper()
	keys := make([]string, len(root.Entries))
	identities := make(map[ids.VersionID]bool)
	for i, entry := range root.Entries {
		if entry.Row == 0 || entry.Version == 0 || identities[entry.Version] {
			t.Fatal("collapsed/invalid identity", entry)
		}
		identities[entry.Version] = true
		value := entry.Keys[0]
		if value.IsNull() {
			keys[i] = "NULL"
		} else {
			var err error
			keys[i], err = value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}

func btreeWantKeys(t *testing.T, e *engine.Engine, name string, want ...string) *storage.IndexRoot {
	t.Helper()
	_, root := btreeRoot(t, e, name)
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	return root
}
