package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

// sourceRead is captured only for an admitted execution source. The shared
// authority slot is detached at Finish, so a retained capture cannot retain a
// completed scope. Its optional cache pin has independent, idempotent lifetime;
// every actual nonempty Open is a fresh statistics request.
type sourceRead struct {
	engine          *Engine
	authority       *exec.Authority
	owner           ids.SessionToken
	ref             ids.ObjectRef
	node            ids.OID
	generation      uint64
	storageAddress  ids.ObjectAddress
	storageRelation *catalog.Relation
	storage         *storage.Relation
	buffer          logicalBufferRead
	hasBuffer       bool
}

func (r *sourceRead) Open() {
	r.record()
	if r.hasBuffer {
		r.buffer.Open()
	}
}
func (r *sourceRead) Close() {
	// Close has no engine/session authority and never reports or publishes work.
	if r.hasBuffer {
		r.buffer.Close()
	}
}
func (r *sourceRead) record() {
	e := r.engine
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if r.authority == nil {
		return
	}
	host, ok := r.authority.Host.(*statementHost)
	if !ok || host == nil {
		return
	}
	s := host.scope()
	if s.Op == nil || s.Op.Session == nil || s.Engine() != e || s.Op.Session.SessionToken != r.owner || EngineOf(s.Op.Session) != e || s.Active() != nil {
		return
	}
	settings := s.Op.EffectiveSettings()
	if settings == nil {
		return
	}
	_, _, policy := settings.StatsConfiguration()
	if !policy.Counts {
		return
	}
	current := e.Catalog.Current()
	database, live := current.Database(e.Catalog.DatabaseAddress())
	if e.Cluster == nil || !live || e.Cluster.Targets[database.Name()] != e || r.ref.Domain() != e.Catalog.Domain || r.ref.Epoch != current.Epoch() || r.generation != e.Statistics.Generation() {
		return
	}
	// Use the current owner's coherent private catalog/storage authority. In
	// particular a genuine TEMP/private CREATE or rewrite need not be committed.
	read := e.PrivateCatalogLocked(s.PendingCatalog())
	relation, exists := read.Relation(r.ref.Address)
	if !exists || relation.EffectiveStorageLocator(read) != r.node {
		return
	}
	parent, exists := read.Relation(r.storageAddress)
	if !exists {
		return
	}
	stored := e.PrivateStorageLocked(s.PendingCatalog(), r.storageAddress)
	if stored == nil || stored != r.storage && !view.SameRetainedLineage(r.storageRelation, parent, r.storage, stored) {
		return
	}
	payload := PayloadOf(s.Op.Session)
	if payload == nil {
		return
	}
	payload.statsWriter.Add(&e.Statistics, statistics.Key{Object: r.ref, Category: statistics.Relation}, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
}

// Caller holds Engine.Mu. The source's actual captured heap incarnation also
// fences its index leases; an index's own locator separately fences REINDEX.
func (e *Engine) captureReadLeaseLocked(scope *StatementScope, source *exec.Source, address ids.ObjectAddress) exec.SourceReadLease {
	if e.Cluster == nil {
		return nil
	}
	data := source.View.Data()
	read := data.Catalog
	relation, exists := read.Relation(address)
	parent, parentExists := read.Relation(source.Ref.Address)
	if !exists || !parentExists {
		return nil
	}
	node := relation.EffectiveStorageLocator(read)
	if node == 0 {
		return nil
	}
	stored := data.GenerationAt(source.Ref.Address)
	if stored == nil {
		stored = e.PrivateStorageLocked(scope.PendingCatalog(), source.Ref.Address)
	}
	if stored == nil {
		// Preserve cache-only source eligibility (for example a nonheap
		// relation) without manufacturing table/index request accounting.
		if key, eligible := e.logicalBufferReadKeyLocked(scope, read, address); eligible {
			return &logicalBufferRead{engine: e, key: key, owner: scope.Op.Session.SessionToken}
		}
		return nil
	}
	ref := source.Ref
	ref.Address = address
	lease := &sourceRead{engine: e, authority: scope.authority, owner: scope.Op.Session.SessionToken,
		ref: ref, node: node, generation: e.Statistics.Generation(),
		storageAddress: source.Ref.Address, storageRelation: parent, storage: stored}
	if key, eligible := e.logicalBufferReadKeyLocked(scope, read, address); eligible {
		lease.hasBuffer = true
		lease.buffer.engine, lease.buffer.key, lease.buffer.owner = e, key, lease.owner
	}
	return lease
}

func (e *Engine) captureReadSourcesLocked(scope *StatementScope, source *exec.Source) {
	if source == nil || source.RlsDenied || source.View == nil {
		return
	}
	if source.Inheritance != nil {
		for _, segment := range source.Inheritance {
			e.captureReadSourcesLocked(scope, segment.Prefix)
		}
		return
	}
	source.ReadLease = e.captureReadLeaseLocked(scope, source, source.Ref.Address)
	for i := range source.HashIndexes {
		source.HashIndexes[i].ReadLease = e.captureReadLeaseLocked(scope, source, source.HashIndexes[i].Address)
	}
	for i := range source.BloomIndexes {
		source.BloomIndexes[i].ReadLease = e.captureReadLeaseLocked(scope, source, source.BloomIndexes[i].Address)
	}
}
