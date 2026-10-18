package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Retain the published maps, not merely a CatalogRead pointing at mutable maps.
type temporaryProjectionCatalog struct {
	view        CatalogRead
	descriptors map[ids.ObjectAddress]Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
}

func captureTemporaryProjectionCatalog(e *Engine) temporaryProjectionCatalog {
	p := temporaryProjectionCatalog{view: e.CatalogRead(), descriptors: make(map[ids.ObjectAddress]Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress)}
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
	if !reflect.DeepEqual(p.view.state.descriptors, p.descriptors) || !reflect.DeepEqual(p.view.state.names, p.names) {
		t.Fatal("replacement/retirement mutated a retained catalog")
	}
}

func assertTemporaryProjectionProviders(t *testing.T, e *Engine) {
	t.Helper()
	view := e.CatalogRead()
	for a, d := range view.Descriptors() {
		if err := e.catalog.providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal("invalid published descriptor", a, err)
		}
		found := view.Lookup(descriptorNameKey(d))
		if len(found) != 1 || found[0] != a {
			t.Fatal("descriptor lost its unique name binding", a, found)
		}
		if r, ok := d.(*relationDescriptor); ok && r.relationKind == sequenceRelation && e.sequences[a] == nil {
			t.Fatal("published sequence has no generator", a)
		}
	}
	for a := range e.sequences {
		r, ok := view.Relation(a)
		if !ok || r.relationKind != sequenceRelation {
			t.Fatal("orphan generator", a)
		}
	}
}

func endTemporaryProjectionSession(t *testing.T, s *Session) {
	t.Helper()
	s.RequestEnd(CleanClose)
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
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE public.anchor(v INT)")
	sessionSQL(t, s, "INSERT INTO public.anchor VALUES(77)")
	sessionSQL(t, s, "CREATE TEMP SEQUENCE local_s CACHE 10")
	ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('local_s')")
	address := description.sequences.constants[0].ref.Address
	namespace, token := s.temporaryNamespace, s.token
	descriptor, nsDescriptor := e.catalog.state.Get(address), e.catalog.state.Get(namespace)
	held := func(want int64) {
		t.Helper()
		r, err := sequencePreparedExecute(e, s, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		got := tuples(r)
		r.Rows.Close()
		if len(got) != 1 || got[0][0] != want {
			t.Fatal("retained analysis lost cached progression", got, want)
		}
	}
	held(1)
	generator, memory := e.sequences[address], s.sequences[address]
	generatorState, memoryState := *generator, *memory
	before := captureTemporaryProjectionCatalog(e)
	sequenceRows := sessionSQL(t, s, "SELECT last_value,is_called FROM local_s")
	tableRows := sessionSQL(t, s, "SELECT v FROM public.anchor")
	for _, want := range []int64{2, 3} {
		if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
			t.Fatal("repeated self replacement", err)
		}
		if s.token != token || s.temporaryNamespace != namespace || e.catalog.state.Get(address) != descriptor || e.catalog.state.Get(namespace) != nsDescriptor || e.sequences[address] != generator || s.sequences[address] != memory || *generator != generatorState || *memory != memoryState {
			t.Fatal("self replacement changed exact live TEMP authority/cache")
		}
		if e.catalog.state.DescriptorCount() != len(before.descriptors) || e.catalog.state.NameCount() != len(before.names) || len(e.sequences) != 1 {
			t.Fatal("self replacement duplicated TEMP identities", e.catalog.state.DescriptorCount(), e.catalog.state.NameCount(), len(e.sequences))
		}
		assertTemporaryProjectionProviders(t, e)
		before.unchanged(t)
		held(want)
		memoryState = *memory
	}
	if got := tuples(sequenceRows); len(got) != 1 || got[0][0] != int64(10) || got[0][1] != true {
		t.Fatal("retained sequence rows changed", got)
	}
	if got := tuples(tableRows); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("retained table rows changed", got)
	}
	endTemporaryProjectionSession(t, s)
	if e.catalog.state.Get(address) != nil || e.sequences[address] != nil {
		t.Fatal("physical end leaked self TEMP children")
	}
	assertTemporaryNamespacePair(t, e, s.token, namespace)
	before.unchanged(t)
	if got := tuples(run(t, e, "SELECT v FROM public.anchor")); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("TEMP retirement removed persistent rows", got)
	}
	assertTemporaryProjectionProviders(t, e)
}

func TestSequenceTemporaryForeignReplacement(t *testing.T) {
	source, dest := New(), New()
	a, b := sessionStart(t, source, nil), sessionStart(t, dest, nil)
	// The source's third user object is a persistent table; destination's
	// third object follows the real table/toast namespace pair and must collide.
	sessionSQL(t, a, "CREATE SCHEMA collision_padding")
	for _, q := range []string{"CREATE SCHEMA portable", "CREATE TABLE portable.owner_table(v INT)", "INSERT INTO portable.owner_table VALUES(7)", "CREATE SEQUENCE portable.owned_s START 40 OWNED BY portable.owner_table.v", "CREATE TEMP SEQUENCE source_temp START 90 CACHE 10"} {
		sessionSQL(t, a, q)
	}
	sessionSQL(t, b, "CREATE TEMP SEQUENCE dest_temp CACHE 10")
	sessionSQL(t, b, "CREATE TABLE public.old_table(v INT)")
	sessionSQL(t, a, "CREATE TABLE portable.survivor(v INT,wide BIGINT,flag BOOL,absent_n BIGINT,absent_b BOOL)")
	sessionSQL(t, a, "INSERT INTO portable.survivor VALUES(8,9223372036854775807,true,NULL,NULL),(9,-9223372036854775808,false,NULL,NULL)")
	sessionSQL(t, b, "INSERT INTO public.old_table VALUES(77)")
	ast, description := sequencePreparedDescription(t, dest, b, "SELECT nextval('dest_temp')")
	address := description.sequences.constants[0].ref.Address
	_, sourceDescription := sequencePreparedDescription(t, source, a, "SELECT nextval('source_temp')")
	sourceTemp := sourceDescription.sequences.constants[0].ref.Address
	sourceTable := roleTableRef(t, source, "portable.owner_table").Address
	if sourceTable != address {
		t.Fatal("fixture did not exercise actual persistent/TEMP OID collision", sourceTable, address)
	}
	sessionSQL(t, a, "CREATE INDEX owner_idx ON portable.owner_table(v)")
	sourceIndex, sourceIndexRoot := btreeRoot(t, source, "portable.owner_idx")
	sequenceScalar(t, a, "SELECT nextval('source_temp')", 90)
	sequenceScalar(t, b, "SELECT nextval('dest_temp')", 1)
	namespace, token := b.temporaryNamespace, b.token
	sourceNamespace := a.temporaryNamespace
	descriptor, nsDescriptor := dest.catalog.state.Get(address), dest.catalog.state.Get(namespace)
	generator, memory := dest.sequences[address], b.sequences[address]
	generatorState, memoryState := *generator, *memory
	sourceGenerator, sourceMemory := source.sequences[sourceTemp], a.sequences[sourceTemp]
	sourceGeneratorState, sourceMemoryState := *sourceGenerator, *sourceMemory
	sourceGenerators := make(map[ids.ObjectAddress]*sequenceGenerator)
	sourceStates := make(map[ids.ObjectAddress]sequenceGenerator)
	for a, g := range source.sequences {
		sourceGenerators[a], sourceStates[a] = g, *g
	}
	sourceBefore, destBefore := captureTemporaryProjectionCatalog(source), captureTemporaryProjectionCatalog(dest)
	sourceSurvivor := source.catalog.state.MustRelation(roleTableRef(t, source, "portable.survivor").Address)
	sourceColumns := append([]ColumnDescriptor(nil), sourceSurvivor.columns...)
	sourceStore := source.storage[sourceSurvivor.address]
	sourceValues := make([][]sqltype.Value, len(sourceStore.versions))
	for i, row := range sourceStore.versions {
		sourceValues[i] = append([]sqltype.Value(nil), row.values...)
	}
	retainedTypedRows := sessionSQL(t, a, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")
	retainedTypes := map[sqltype.ID]*sqltype.Descriptor{
		sqltype.Int8: descriptor.(*relationDescriptor).columns[0].Usage.Type,
		sqltype.Bool: descriptor.(*relationDescriptor).columns[2].Usage.Type,
	}
	oldRows := sessionSQL(t, b, "SELECT v FROM public.old_table")
	sourceRows := sessionSQL(t, a, "SELECT v FROM portable.owner_table")
	if _, err := dest.ReplaceCatalog(context.Background(), source, sourceBefore.view); err != nil {
		t.Fatal(err)
	}
	if b.token != token || b.temporaryNamespace != namespace || dest.catalog.state.Get(address) != descriptor || dest.catalog.state.Get(namespace) != nsDescriptor || dest.sequences[address] != generator || b.sequences[address] != memory || *generator != generatorState || *memory != memoryState {
		t.Fatal("foreign replacement changed destination TEMP identity/cache")
	}
	var tempNamespaces, tempRelations int
	toast := assertTemporaryNamespacePair(t, dest, token, namespace)
	toastDescriptor := destBefore.view.Get(toast)
	if toastDescriptor == nil || dest.catalog.state.Get(toast) != toastDescriptor {
		t.Fatal("foreign replacement changed destination toast identity")
	}
	for _, d := range dest.catalog.state.Descriptors() {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary != (ids.SessionToken{}) {
			tempNamespaces++
			if ns.temporary != b.token || (ns.address != namespace && ns.address != toast) || d != destBefore.view.Get(ns.address) {
				t.Fatal("imported foreign SessionToken", ns.temporary)
			}
		}
		if d.Namespace() == namespace {
			tempRelations++
		}
		if d.Name() == "source_temp" {
			t.Fatal("imported source TEMP descriptor")
		}
	}
	if tempNamespaces != 2 || tempRelations != 1 || len(dest.sequences) != 2 {
		t.Fatal("foreign replacement leaked TEMP descriptors/generators", tempNamespaces, tempRelations, len(dest.sequences))
	}
	importedSurvivor := dest.catalog.state.MustRelation(roleTableRef(t, dest, "portable.survivor").Address)
	for id, retainedType := range retainedTypes {
		actual, ok := dest.CatalogRead().LookupType(id)
		if !ok || actual != retainedType {
			t.Fatal("foreign replacement lost retained TEMP builtin dependency", id)
		}
	}
	for _, column := range importedSurvivor.columns {
		actual, ok := dest.CatalogRead().LookupType(column.Usage.Type.ID())
		if !ok || actual != column.Usage.Type {
			t.Fatal("imported column uses a foreign type generation", column.Name)
		}
	}
	for _, row := range dest.storage[importedSurvivor.address].versions {
		for _, value := range row.values {
			actual, ok := dest.CatalogRead().LookupType(value.Type().ID())
			if !ok || actual != value.Type() {
				t.Fatal("imported value uses a foreign type generation", value.Type().ID())
			}
		}
	}
	if !reflect.DeepEqual(sourceSurvivor.columns, sourceColumns) {
		t.Fatal("type rebinding mutated source descriptor columns")
	}
	for i, column := range sourceSurvivor.columns {
		if column.Usage.Type != sourceColumns[i].Usage.Type {
			t.Fatal("type rebinding changed source column type pointer")
		}
	}
	for i, row := range sourceStore.versions {
		if !reflect.DeepEqual(row.values, sourceValues[i]) {
			t.Fatal("type rebinding mutated source values")
		}
		for j, value := range row.values {
			if value.Type() != sourceValues[i][j].Type() {
				t.Fatal("type rebinding changed source value type pointer")
			}
		}
	}
	assertTemporaryProjectionTypedRows(t, tuples(retainedTypedRows))
	assertTemporaryProjectionProviders(t, dest)
	sourceBefore.unchanged(t)
	for a, g := range sourceGenerators {
		if source.sequences[a] != g || *g != sourceStates[a] {
			t.Fatal("foreign import changed source generator", a)
		}
	}
	destBefore.unchanged(t)
	if source.catalog.state != sourceBefore.view.state || source.sequences[sourceTemp] != sourceGenerator || a.sequences[sourceTemp] != sourceMemory || *sourceGenerator != sourceGeneratorState || *sourceMemory != sourceMemoryState {
		t.Fatal("destination import changed source live authority")
	}
	r, err := sequencePreparedExecute(dest, b, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	if got := tuples(r); len(got) != 1 || got[0][0] != int64(2) {
		t.Fatal("held destination cache did not survive foreign replacement", got)
	}
	r.Rows.Close()
	importedTable := roleTableRef(t, dest, "portable.owner_table").Address
	importedSequence := roleTableRef(t, dest, "portable.owned_s").Address
	if importedTable == sourceTable || dest.catalog.state.MustRelation(importedSequence).sequence.owned != (ids.ObjectAddress{ClassID: importedTable.ClassID, ObjectID: importedTable.ObjectID, SubID: 1}) {
		t.Fatal("persistent collision did not remap OWNED BY", importedTable, dest.catalog.state.Get(importedSequence))
	}
	sequenceScalar(t, b, "SELECT nextval('portable.owned_s')", 40)
	if got := tuples(sessionSQL(t, b, "SELECT v FROM portable.owner_table")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("remapped persistent rows missing", got)
	}
	index, indexRoot := btreeRoot(t, dest, "portable.owner_idx")
	if index.sqlIndex.parent != importedTable || sourceIndex.sqlIndex.parent != sourceTable || index.sqlIndex == sourceIndex.sqlIndex {
		t.Fatal("index parent collision remapping lost source/destination authority")
	}
	if got := btreeKeys(t, indexRoot); !reflect.DeepEqual(got, []string{"7"}) {
		t.Fatal("reminted index membership", got)
	}
	sessionSQL(t, b, "INSERT INTO portable.owner_table VALUES(8)")
	btreeWantKeys(t, dest, "portable.owner_idx", "7", "8")
	if got := btreeKeys(t, sourceIndexRoot); !reflect.DeepEqual(got, []string{"7"}) {
		t.Fatal("destination DML mutated source root", got)
	}
	if got := tuples(oldRows); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatal("retained destination rows changed", got)
	}
	endTemporaryProjectionSession(t, a)
	if source.catalog.state.Get(sourceTemp) != nil || source.sequences[sourceTemp] != nil {
		t.Fatal("source retirement leaked TEMP children")
	}
	assertTemporaryNamespacePair(t, source, a.token, sourceNamespace)
	sequenceScalar(t, b, "SELECT nextval('dest_temp')", 3)
	if dest.sequences[address] != generator {
		t.Fatal("source retirement affected destination TEMP generator")
	}
	if got := tuples(sourceRows); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("source retirement changed retained rows", got)
	}
	sessionSQL(t, b, "DROP TABLE portable.owner_table")
	if dest.catalog.state.Get(importedTable) != nil || dest.catalog.state.Get(importedSequence) != nil || dest.sequences[importedSequence] != nil {
		t.Fatal("remapped ownership drop closure failed")
	}
	if dest.catalog.state.Get(index.address) != nil || dest.indexRoots[index.address] != nil {
		t.Fatal("remapped parent drop left index definition/root")
	}
	if source.catalog.state.Get(sourceTable) == nil {
		t.Fatal("destination drop changed source persistent catalog")
	}
	endTemporaryProjectionSession(t, b)
	if dest.catalog.state.Get(address) != nil || dest.sequences[address] != nil {
		t.Fatal("destination retirement leaked TEMP")
	}
	assertTemporaryNamespacePair(t, dest, b.token, namespace)
	if got := tuples(run(t, dest, "SELECT v FROM portable.survivor ORDER BY v")); len(got) != 2 || got[0][0] != int64(8) || got[1][0] != int64(9) {
		t.Fatal("destination retirement removed persistent survivors", got)
	}
	assertTemporaryProjectionTypedRows(t, tuples(run(t, dest, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")))
	assertTemporaryProjectionTypedRows(t, tuples(run(t, source, "SELECT wide,flag,absent_n,absent_b FROM portable.survivor ORDER BY v")))
	if got := tuples(run(t, source, "SELECT v FROM portable.owner_table")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal("source retirement/drop removed persistent rows", got)
	}
	if got := tuples(run(t, source, "SELECT nextval('portable.owned_s')")); len(got) != 1 || got[0][0] != int64(40) {
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
			source := New()
			s := sessionStart(t, source, nil)
			sessionSQL(t, s, "CREATE TABLE public.kept(v INT)")
			sessionSQL(t, s, "INSERT INTO public.kept VALUES(7)")
			sessionSQL(t, s, "CREATE SEQUENCE public.kept_s START 40")
			sessionSQL(t, s, "CREATE TEMP SEQUENCE retired_temp START 90")
			_, description := sequencePreparedDescription(t, source, s, "SELECT nextval('retired_temp')")
			address, namespace := description.sequences.constants[0].ref.Address, s.temporaryNamespace
			captured := captureTemporaryProjectionCatalog(source)
			retained := sessionSQL(t, s, "SELECT last_value FROM retired_temp")
			endTemporaryProjectionSession(t, s)
			if source.sequences[address] != nil || source.catalog.state.Get(address) != nil {
				t.Fatal("fixture TEMP generator/relation was not retired")
			}
			toast := assertTemporaryNamespacePair(t, source, s.token, namespace)
			tableDescriptor, toastDescriptor := source.catalog.state.Get(namespace), source.catalog.state.Get(toast)
			dest := source
			if !self {
				dest = New()
			}
			if _, err := dest.ReplaceCatalog(context.Background(), source, captured.view); err != nil {
				t.Fatal("excluded retired TEMP required a deleted generator", err)
			}
			for _, d := range dest.catalog.state.Descriptors() {
				if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary != (ids.SessionToken{}) {
					if !self || (ns.address != namespace && ns.address != toast) {
						t.Fatal("imported foreign TEMP namespace")
					}
					if dest.catalog.state.Get(ns.address) != source.catalog.state.Get(ns.address) {
						t.Fatal("replaced retained empty namespace")
					}
				}
				if d.Name() == "retired_temp" {
					t.Fatal("resurrected retired TEMP relation")
				}
			}
			if self {
				if dest.catalog.state.Get(namespace) != tableDescriptor || dest.catalog.state.Get(toast) != toastDescriptor || s.temporaryNamespace != (ids.ObjectAddress{}) {
					t.Fatal("self replacement changed retained pair/retired authority")
				}
			} else if dest.catalog.state.Get(namespace) != nil || dest.catalog.state.Get(toast) != nil {
				t.Fatal("foreign replacement imported retired source pair")
			}
			if len(dest.sequences) != 1 {
				t.Fatal("retired TEMP generator imported", len(dest.sequences))
			}
			if got := tuples(retained); len(got) != 1 || got[0][0] != int64(90) {
				t.Fatal("retained retired TEMP rows changed", got)
			}
			if got := tuples(run(t, dest, "SELECT v FROM public.kept")); len(got) != 1 || got[0][0] != int64(7) {
				t.Fatal("persistent source rows missing", got)
			}
			if got := tuples(run(t, dest, "SELECT nextval('public.kept_s')")); len(got) != 1 || got[0][0] != int64(40) {
				t.Fatal("persistent source generator missing", got)
			}
			captured.unchanged(t)
			assertTemporaryProjectionProviders(t, dest)
		})
	}
}
