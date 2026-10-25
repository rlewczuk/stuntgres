package tablecmds_test

import (
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
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
	memory                  map[ids.ObjectAddress]*sequence.Memory
	memoryValues            map[ids.ObjectAddress]sequence.Memory
}

func captureSequenceDeletePublication(e *core.Engine, s *session.Session, table ids.ObjectAddress) sequenceDeletePublicationSnapshot {
	p := sequenceDeletePublicationSnapshot{
		state: e.Catalog.State, catalog: *e.Catalog.State,
		descriptors: make(map[ids.ObjectAddress]catalog.Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress),
		storageID:       uintptr(reflect.ValueOf(e.Storage.Relations).UnsafePointer()),
		generatorsID:    uintptr(reflect.ValueOf(e.Storage.Sequences).UnsafePointer()),
		stores:          make(map[ids.ObjectAddress]*storage.Relation),
		generators:      make(map[ids.ObjectAddress]*storage.SequenceGenerator),
		generatorValues: make(map[ids.ObjectAddress]storage.SequenceGenerator),
		store:           e.Storage.Relations[table], frontier: e.Visibility.CommitSeq(),
		temporary: s.TemporaryNamespace, last: core.PayloadOf(s).Sequences.Last(),
		memory: make(map[ids.ObjectAddress]*sequence.Memory), memoryValues: make(map[ids.ObjectAddress]sequence.Memory),
	}
	for a, d := range e.Catalog.State.Descriptors() {
		p.descriptors[a] = d
	}
	for k, a := range e.Catalog.State.Names() {
		p.names[k] = a
	}
	for a, store := range e.Storage.Relations {
		p.stores[a] = store
	}
	for a, g := range e.Storage.Sequences {
		p.generators[a], p.generatorValues[a] = g, *g
	}
	for a, m := range core.PayloadOf(s).Sequences.All() {
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

func (p sequenceDeletePublicationSnapshot) assertUnchanged(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	if e.Catalog.State != p.state || !reflect.DeepEqual(*e.Catalog.State, p.catalog) || !reflect.DeepEqual(maps.Collect(e.Catalog.State.Descriptors()), p.descriptors) || !reflect.DeepEqual(maps.Collect(e.Catalog.State.Names()), p.names) {
		t.Fatal("failed combined publication changed catalog authority, descriptor/name bindings or namespace generation")
	}
	if uintptr(reflect.ValueOf(e.Storage.Relations).UnsafePointer()) != p.storageID || uintptr(reflect.ValueOf(e.Storage.Sequences).UnsafePointer()) != p.generatorsID || len(e.Storage.Relations) != len(p.stores) || len(e.Storage.Sequences) != len(p.generators) {
		t.Fatal("failed combined publication replaced or resized shared storage/generator containers")
	}
	for a, store := range p.stores {
		if e.Storage.Relations[a] != store {
			t.Fatal("changed storage identity", a)
		}
	}
	for a, g := range p.generators {
		if e.Storage.Sequences[a] != g || *g != p.generatorValues[a] {
			t.Fatal("changed generator identity/state", a)
		}
	}
	if len(p.store.Versions) != len(p.root) || &p.store.Versions[0] != &p.root[0] || !reflect.DeepEqual(p.store.Versions, p.rows) || p.store.Ends != p.ends || e.Visibility.CommitSeq() != p.frontier {
		t.Fatal("failed combined publication changed row/version vector, predecessor root or frontier")
	}
	if p.ends != nil && !reflect.DeepEqual(p.ends.Rows, p.endValues) {
		t.Fatal("changed committed deletion entries")
	}
	if s.TemporaryNamespace != p.temporary || core.PayloadOf(s).Sequences.Last() != p.last || core.PayloadOf(s).Sequences.Len() != len(p.memory) {
		t.Fatal("failed combined publication changed session temporary handle or sequence memory container")
	}
	for a, m := range p.memory {
		if core.PayloadOf(s).Sequences.Memory(a) != m || *m != p.memoryValues[a] {
			t.Fatal("changed session sequence memory", a)
		}
	}
}

func assertSequenceDeletePrivateBatch(t *testing.T, s *session.Session, table, sequence ids.ObjectAddress, firstTemp bool) {
	t.Helper()
	tx := s.Transactions
	if tx.State != session.TxActive || len(tx.Writes[table]) != 2 || tx.Deletes[table] == nil || len(tx.Deletes[table].Rows) != 3 || tx.Catalog == nil {
		t.Fatal("fixture must contain private appended rows, committed/own deletions and sequence overlay")
	}
	g := tx.Catalog.Generators[sequence]
	if g == nil || g.Last != 100 || g.Called || g == core.EngineOf(s).Storage.Sequences[sequence] {
		t.Fatal("fixture must contain a private restarted generator")
	}
	if firstTemp {
		if s.TemporaryNamespace != (ids.ObjectAddress{}) {
			t.Fatal("fixture reused a published TEMP namespace")
		}
		var namespaces, temps int
		for a, d := range tx.Catalog.Descriptors {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == s.SessionToken {
				namespaces++
				if tx.Catalog.Originals[a] != nil || core.EngineOf(s).Catalog.State.Get(a) != nil {
					t.Fatal("TEMP namespace was not genuinely new")
				}
			}
			if r, ok := d.(*catalog.Relation); ok && r.Name() == "first_temp" {
				temps++
				if tx.Catalog.Generators[a] == nil || core.EngineOf(s).Storage.Sequences[a] != nil {
					t.Fatal("TEMP generator is not private")
				}
			}
		}
		if namespaces != 2 || temps != 1 {
			t.Fatal("missing genuine first TEMP namespace/sequence", namespaces, temps)
		}
	}
}

func assertSequenceDeleteEndCleanup(t *testing.T, e *core.Engine, s *session.Session, old *lock.Ticket) {
	t.Helper()
	tx := s.Transactions
	if tx.State != session.TxIdle || tx.Ref != (ids.TxRef{}) || tx.Command != 0 || !tx.Start.IsZero() || tx.Writes != nil || tx.Deletes != nil || tx.Catalog != nil || tx.Ticket != nil {
		t.Fatal("failed native Commit did not discard the complete private transaction incarnation")
	}
	if len(e.Locks.Inspect(old).Journals[old]) != 0 {
		t.Fatal("failed transaction retained ticket resources")
	}
	enginetest.LockEmpty(t, e)
}
