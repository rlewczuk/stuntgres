package engine

import (
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// These snapshots distinguish container replacement from in-place mutation.
// Values are copied only in this test fixture; production roots remain retained.
type sequenceDeletePublicationSnapshot struct {
	state                   *catalog.State
	catalog                 catalog.State
	descriptors             map[ids.ObjectAddress]catalog.Descriptor
	names                   map[ids.NameKey]ids.ObjectAddress
	storageID, generatorsID uintptr
	stores                  map[ids.ObjectAddress]*storage.Relation
	generators              map[ids.ObjectAddress]*storage.SequenceGenerator
	generatorValues         map[ids.ObjectAddress]storage.SequenceGenerator
	store                   *storage.Relation
	root                    []storage.RowVersion
	rows                    []storage.RowVersion
	ends                    *storage.DeletionRoot
	endValues               map[ids.RowID]storage.RowEnd
	frontier                ids.CommitSeq
	temporary, last         ids.ObjectAddress
	memoryID                uintptr
	memory                  map[ids.ObjectAddress]*sequenceMemory
	memoryValues            map[ids.ObjectAddress]sequenceMemory
}

func captureSequenceDeletePublication(e *Engine, s *Session, table ids.ObjectAddress) sequenceDeletePublicationSnapshot {
	p := sequenceDeletePublicationSnapshot{
		state: e.catalog.state, catalog: *e.catalog.state,
		descriptors: make(map[ids.ObjectAddress]catalog.Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress),
		storageID:       uintptr(reflect.ValueOf(e.storage.Relations).UnsafePointer()),
		generatorsID:    uintptr(reflect.ValueOf(e.storage.Sequences).UnsafePointer()),
		stores:          make(map[ids.ObjectAddress]*storage.Relation),
		generators:      make(map[ids.ObjectAddress]*storage.SequenceGenerator),
		generatorValues: make(map[ids.ObjectAddress]storage.SequenceGenerator),
		store:           e.storage.Relations[table], frontier: e.visibility.commitSeq,
		temporary: s.temporaryNamespace, last: s.lastSequence,
		memoryID: uintptr(reflect.ValueOf(s.sequences).UnsafePointer()),
		memory:   make(map[ids.ObjectAddress]*sequenceMemory), memoryValues: make(map[ids.ObjectAddress]sequenceMemory),
	}
	for a, d := range e.catalog.state.Descriptors() {
		p.descriptors[a] = d
	}
	for k, a := range e.catalog.state.Names() {
		p.names[k] = a
	}
	for a, store := range e.storage.Relations {
		p.stores[a] = store
	}
	for a, g := range e.storage.Sequences {
		p.generators[a], p.generatorValues[a] = g, *g
	}
	for a, m := range s.sequences {
		p.memory[a], p.memoryValues[a] = m, *m
	}
	p.root, p.ends = p.store.Versions, p.store.Ends
	p.rows = append([]storage.RowVersion(nil), p.root...)
	for i := range p.rows {
		p.rows[i].Values = append([]sqltype.Value(nil), p.rows[i].Values...)
	}
	if p.ends != nil {
		p.endValues = make(map[ids.RowID]storage.RowEnd)
		for id, end := range p.ends.Rows {
			p.endValues[id] = end
		}
	}
	return p
}

func (p sequenceDeletePublicationSnapshot) assertUnchanged(t *testing.T, e *Engine, s *Session) {
	t.Helper()
	if e.catalog.state != p.state || !reflect.DeepEqual(*e.catalog.state, p.catalog) || !reflect.DeepEqual(maps.Collect(e.catalog.state.Descriptors()), p.descriptors) || !reflect.DeepEqual(maps.Collect(e.catalog.state.Names()), p.names) {
		t.Fatal("failed combined publication changed catalog authority, descriptor/name bindings or namespace generation")
	}
	if uintptr(reflect.ValueOf(e.storage.Relations).UnsafePointer()) != p.storageID || uintptr(reflect.ValueOf(e.storage.Sequences).UnsafePointer()) != p.generatorsID || len(e.storage.Relations) != len(p.stores) || len(e.storage.Sequences) != len(p.generators) {
		t.Fatal("failed combined publication replaced or resized shared storage/generator containers")
	}
	for a, store := range p.stores {
		if e.storage.Relations[a] != store {
			t.Fatal("changed storage identity", a)
		}
	}
	for a, g := range p.generators {
		if e.storage.Sequences[a] != g || *g != p.generatorValues[a] {
			t.Fatal("changed generator identity/state", a)
		}
	}
	if len(p.store.Versions) != len(p.root) || &p.store.Versions[0] != &p.root[0] || !reflect.DeepEqual(p.store.Versions, p.rows) || p.store.Ends != p.ends || e.visibility.commitSeq != p.frontier {
		t.Fatal("failed combined publication changed row/version vector, predecessor root or frontier")
	}
	if p.ends != nil && !reflect.DeepEqual(p.ends.Rows, p.endValues) {
		t.Fatal("changed committed deletion entries")
	}
	if s.temporaryNamespace != p.temporary || s.lastSequence != p.last || uintptr(reflect.ValueOf(s.sequences).UnsafePointer()) != p.memoryID || len(s.sequences) != len(p.memory) {
		t.Fatal("failed combined publication changed session temporary handle or sequence memory container")
	}
	for a, m := range p.memory {
		if s.sequences[a] != m || *m != p.memoryValues[a] {
			t.Fatal("changed session sequence memory", a)
		}
	}
}

func assertSequenceDeletePrivateBatch(t *testing.T, s *Session, table, sequence ids.ObjectAddress, firstTemp bool) {
	t.Helper()
	tx := s.transactions
	if tx.state != TxActive || len(tx.writes[table]) != 2 || tx.deletes[table] == nil || len(tx.deletes[table].rows) != 3 || tx.catalog == nil {
		t.Fatal("fixture must contain private appended rows, committed/own deletions and sequence overlay")
	}
	g := tx.catalog.generators[sequence]
	if g == nil || g.Last != 100 || g.Called || g == s.Engine().storage.Sequences[sequence] {
		t.Fatal("fixture must contain a private restarted generator")
	}
	if firstTemp {
		if s.temporaryNamespace != (ids.ObjectAddress{}) {
			t.Fatal("fixture reused a published TEMP namespace")
		}
		var namespaces, temps int
		for a, d := range tx.catalog.descriptors {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == s.token {
				namespaces++
				if tx.catalog.originals[a] != nil || s.Engine().catalog.state.Get(a) != nil {
					t.Fatal("TEMP namespace was not genuinely new")
				}
			}
			if r, ok := d.(*catalog.Relation); ok && r.Name() == "first_temp" {
				temps++
				if tx.catalog.generators[a] == nil || s.Engine().storage.Sequences[a] != nil {
					t.Fatal("TEMP generator is not private")
				}
			}
		}
		if namespaces != 2 || temps != 1 {
			t.Fatal("missing genuine first TEMP namespace/sequence", namespaces, temps)
		}
	}
}

func assertSequenceDeleteEndCleanup(t *testing.T, e *Engine, s *Session, old *lock.Ticket) {
	t.Helper()
	tx := s.transactions
	if tx.state != TxIdle || tx.ref != (ids.TxRef{}) || tx.command != 0 || !tx.start.IsZero() || tx.writes != nil || tx.deletes != nil || tx.catalog != nil || tx.ticket != nil {
		t.Fatal("failed native Commit did not discard the complete private transaction incarnation")
	}
	if len(e.locks.Inspect(old).Journals[old]) != 0 {
		t.Fatal("failed transaction retained ticket resources")
	}
	lockEmpty(t, e)
}
