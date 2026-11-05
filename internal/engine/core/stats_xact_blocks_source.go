package core

import (
	"context"
	"sync/atomic"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
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
	workerEpoch     atomic.Uint64 // Close fences observers without worker scope access.
}

func (r *sourceRead) Open() {
	r.record()
}
func (r *sourceRead) Close() {
	// Close has no engine/session authority and never reports or publishes work.
	r.workerEpoch.Add(1)
	if r.hasBuffer {
		r.buffer.Close()
	}
}
func (r *sourceRead) record() {
	e := r.engine
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s, read, relation, _ := r.admissionLocked()
	if s == nil {
		return
	}
	counters := statistics.StatementCounters{}
	if r.hasBuffer {
		valid, resident := r.buffer.openLocked()
		if !valid {
			return
		}
		if resident {
			counters.SharedHit = 1
		} else {
			counters.SharedRead = 1
		}
	} else if table.EffectivePersistence(read, relation) == "temporary" {
		counters.LocalHit = 1
	} else {
		counters.SharedHit = 1
	}
	s.recordStatementCounters(counters)
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts || r.generation != e.Statistics.Generation() {
		return
	}
	payload := PayloadOf(s.Op.Session)
	if payload == nil {
		return
	}
	payload.statsWriter.Add(&e.Statistics, statistics.Key{Object: r.ref, Category: statistics.Relation}, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
}

// Coordinator-only admission: lexical scope, settings and private journal are
// never inspected by a parallel worker.
func (r *sourceRead) admissionLocked() (*StatementScope, catalog.Read, *catalog.Relation, *storage.Relation) {
	e := r.engine
	if r.authority == nil {
		return nil, catalog.Read{}, nil, nil
	}
	host, ok := r.authority.Host.(*statementHost)
	if !ok || host == nil {
		return nil, catalog.Read{}, nil, nil
	}
	s := host.scope()
	if s.Op == nil || s.Op.Session == nil || s.Engine() != e || s.Op.Session.SessionToken != r.owner || EngineOf(s.Op.Session) != e || s.Active() != nil || s.Op.EffectiveSettings() == nil {
		return nil, catalog.Read{}, nil, nil
	}
	current := e.Catalog.Current()
	database, live := current.Database(e.Catalog.DatabaseAddress())
	if e.Cluster == nil || !live || e.Cluster.Targets[database.Name()] != e || r.ref.Domain() != e.Catalog.Domain || r.ref.Epoch != current.Epoch() {
		return nil, catalog.Read{}, nil, nil
	}
	read := e.PrivateCatalogLocked(s.PendingCatalog())
	relation, exists := read.Relation(r.ref.Address)
	if !exists || relation.EffectiveStorageLocator(read) != r.node {
		return nil, catalog.Read{}, nil, nil
	}
	if ns, ok := read.Namespace(relation.Namespace()); !ok || ns.Temporary != (ids.SessionToken{}) && ns.Temporary != r.owner {
		return nil, catalog.Read{}, nil, nil
	}
	parent, exists := read.Relation(r.storageAddress)
	if !exists {
		return nil, catalog.Read{}, nil, nil
	}
	stored := e.PrivateStorageLocked(s.PendingCatalog(), r.storageAddress)
	if stored == nil || stored != r.storage && !view.SameRetainedLineage(r.storageRelation, parent, r.storage, stored) {
		return nil, catalog.Read{}, nil, nil
	}
	return s, read, relation, stored
}

// Workers borrow immutable admission and the capture-owned pin, never mutable
// lexical scope. MergeWorker alone resolves lexical authority after join.
type sourceReadWorker struct {
	engine          *Engine
	buffer          *logicalBufferRead
	capture         *atomic.Uint64
	ref             ids.ObjectRef
	node            ids.OID
	generation      uint64
	storageAddress  ids.ObjectAddress
	storageRelation *catalog.Relation
	storage         *storage.Relation
	context  context.Context
	epoch    uint64
	private  bool
	local    bool
	counts   bool
	active   bool
	counters statistics.StatementCounters
	requests int64
}

func (r *sourceRead) CaptureWorker() exec.SourceReadLease {
	e := r.engine
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s, read, relation, stored := r.admissionLocked()
	if s == nil {
		return nil
	}
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	worker := &sourceReadWorker{engine: e, capture: &r.workerEpoch,
		ref: r.ref, node: r.node, generation: r.generation,
		storageAddress: r.storageAddress, storageRelation: r.storageRelation, storage: r.storage,
		context: s.Op.Context(), epoch: r.workerEpoch.Load(), active: true, counts: policy.Counts,
		private: e.Storage.Relations[r.storageAddress] != stored,
		local: table.EffectivePersistence(read, relation) == "temporary"}
	if r.hasBuffer {
		worker.buffer = &r.buffer
	}
	return worker
}

func (w *sourceReadWorker) Open() {
	if !w.active {
		return
	}
	if w.context.Err() != nil || w.capture.Load() != w.epoch {
		return
	}
	e := w.engine
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current := e.Catalog.Current()
	database, live := current.Database(e.Catalog.DatabaseAddress())
	if e.Cluster == nil || !live || e.Cluster.Targets[database.Name()] != e || w.ref.Domain() != e.Catalog.Domain || w.ref.Epoch != current.Epoch() {
		return
	}
	// The admitted command's private journal is stable across joined worker
	// execution. Committed storage may gain compatible retained headers;
	// validate its actual family at each Open.
	if !w.private {
		relation, exists := current.Relation(w.ref.Address)
		if !exists || relation.EffectiveStorageLocator(current) != w.node {
			return
		}
		parent, exists := current.Relation(w.storageAddress)
		stored := e.Storage.Relations[w.storageAddress]
		if !exists || stored == nil || stored != w.storage && !view.SameRetainedLineage(w.storageRelation, parent, w.storage, stored) {
			return
		}
	}
	if w.buffer != nil {
		valid, resident := w.buffer.openCacheLocked()
		if !valid {
			return
		}
		if resident {
			w.counters.SharedHit++
		} else {
			w.counters.SharedRead++
		}
	} else if w.local {
		w.counters.LocalHit++
	} else {
		w.counters.SharedHit++
	}
	if w.counts && w.generation == e.Statistics.Generation() {
		w.requests++
	}
}

func (w *sourceReadWorker) Close() {} // The original source alone owns the pin.

func (r *sourceRead) MergeWorker(worker exec.SourceReadLease) {
	w, ok := worker.(*sourceReadWorker)
	if !ok || w.capture != &r.workerEpoch || !w.active {
		return
	}
	w.active = false
	w.engine, w.buffer, w.capture = nil, nil, nil
	w.storageRelation, w.storage = nil, nil
	w.context = nil
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
	if s.Finished || s.Op == nil || s.Op.Released || s.Engine() != e || s.Op.Session.SessionToken != r.owner {
		return
	}
	s.recordStatementCounters(w.counters)
	if w.requests != 0 && r.generation == e.Statistics.Generation() {
		if payload := PayloadOf(s.Op.Session); payload != nil {
			payload.statsWriter.Add(&e.Statistics, statistics.Key{Object: r.ref, Category: statistics.Relation}, statistics.Fact{BlocksFetched: w.requests, BlocksHit: w.requests})
		}
	}
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
			buffer := &logicalBufferRead{engine: e, key: key, owner: scope.Op.Session.SessionToken, authority: scope.authority}
			if relation.SQLIndex != nil {
				buffer.op = scope.Op
				buffer.ref = e.Catalog.Domain.Ref(read.Epoch(), address)
			}
			return buffer
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
		if relation.SQLIndex != nil {
			lease.buffer.op = scope.Op
			lease.buffer.ref = e.Catalog.Domain.Ref(read.Epoch(), address)
		}
	}
	return lease
}

func (e *Engine) captureReadSourcesLocked(scope *StatementScope, source *exec.Source) {
	if source == nil || source.RlsDenied || source.View == nil {
		return
	}
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: HostOf(scope), RoutineCatalog: scope.RoutineAuth.Catalog}
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
