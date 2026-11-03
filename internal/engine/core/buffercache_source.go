package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"sync"
)

// A source owns this bridge, never the cache. Admission captures scalar identity;
// actual Open revalidates committed incarnation under Engine.Mu before entering
// the leaf cache mutex. No preparation/catalog scan increments read usage.
type logicalBufferRead struct {
	// Engine.Mu precedes this mutex; Close takes only this mutex then the
	// cache leaf. Parallel scan invocations share one capture-owned lease.
	mu     sync.Mutex
	engine *Engine
	key    logicalBufferKey
	owner  ids.SessionToken
	cache  *logicalBufferCache
	entry  *logicalBufferEntry
	opened bool
}

func (r *logicalBufferRead) Open() {
	e := r.engine
	e.Mu.Lock()
	defer e.Mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	// Unpublished template runtimes have no cluster. A bridge from a retired
	// runtime cannot acquire a shared cache owner before attachment.
	if e.Cluster == nil {
		return
	}
	view := e.Catalog.Current()
	database, exists := view.Database(e.Catalog.DatabaseAddress())
	if !exists || e.Cluster.Targets[database.Name()] != e || view.Epoch() != r.key.epoch {
		return
	}
	relation, ok := view.Relation(r.key.relation)
	if !ok {
		return
	}
	key, eligible := logicalBufferIdentity(view, relation)
	if !eligible || key != r.key {
		return
	}
	if r.opened && r.entry != nil {
		if r.cache != nil {
			r.cache.touch(r.entry)
		}
		return
	}
	r.opened = true
	r.cache = e.logicalBuffersLocked()
	r.entry = r.cache.open(r.key, r.owner)
}
func (r *logicalBufferRead) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache != nil {
		r.cache.close(r.entry, r.owner)
	}
	r.cache, r.entry, r.opened = nil, nil, false
}

// Caller holds Engine.Mu and has validated a registered cluster owner.
func (e *Engine) logicalBuffersLocked() *logicalBufferCache {
	if e.Cluster.buffercache == nil {
		definition, _ := e.Settings.Lookup("shared_buffers")
		e.Cluster.buffercache = &logicalBufferCache{capacity: int64(definition.DefaultValue().Number)}
	}
	return e.Cluster.buffercache
}
func logicalBufferIdentity(view catalog.Read, relation *catalog.Relation) (logicalBufferKey, bool) {
	if relation == nil || view.Owner() == nil {
		return logicalBufferKey{}, false
	}
	namespace, ok := view.Namespace(relation.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return logicalBufferKey{}, false
	}
	node := relation.EffectiveStorageLocator(view)
	if node == 0 {
		return logicalBufferKey{}, false
	}
	key := logicalBufferKey{domain: view.Owner().Domain, epoch: view.Epoch(), relation: relation.Address(), node: node, space: catalog.DefaultTablespaceOID, database: view.Owner().DatabaseAddress().ObjectID}
	if catalog.SharedRelation(relation) {
		key.space, key.database = catalog.GlobalTablespaceOID, 0
	}
	return key, true
}
func (e *Engine) captureLogicalBufferReadLocked(scope *StatementScope, view catalog.Read, address ids.ObjectAddress) exec.SourceReadLease {
	// CREATE DATABASE's private replacement target is not an execution/cache
	// owner until the database is published and attached to its real cluster.
	if e.Cluster == nil {
		return nil
	}
	if journal := scope.PendingCatalog(); journal != nil && journal.Storage[address].Rewritten {
		return nil
	}
	relation, ok := view.Relation(address)
	if !ok {
		return nil
	}
	key, eligible := logicalBufferIdentity(view, relation)
	if !eligible {
		return nil
	}
	committed, ok := e.Catalog.Current().Relation(address)
	if !ok {
		return nil
	}
	incumbent, eligible := logicalBufferIdentity(e.Catalog.Current(), committed)
	if !eligible || incumbent != key {
		return nil
	}
	return &logicalBufferRead{engine: e, key: key, owner: scope.Op.Session.SessionToken}
}
func (e *Engine) captureLogicalBufferSourcesLocked(scope *StatementScope, source *exec.Source) {
	if source == nil || source.RlsDenied || source.View == nil {
		return
	}
	if source.Inheritance != nil {
		for _, segment := range source.Inheritance {
			e.captureLogicalBufferSourcesLocked(scope, segment.Prefix)
		}
		return
	}
	view := source.View.Data().Catalog
	source.ReadLease = e.captureLogicalBufferReadLocked(scope, view, source.Ref.Address)
	for i := range source.HashIndexes {
		source.HashIndexes[i].ReadLease = e.captureLogicalBufferReadLocked(scope, view, source.HashIndexes[i].Address)
	}
	for i := range source.BloomIndexes {
		source.BloomIndexes[i].ReadLease = e.captureLogicalBufferReadLocked(scope, view, source.BloomIndexes[i].Address)
	}
}

// Successful publication reconciles sparse occupancy only. Descriptor revisions
// (rename/owner/ACL) do not replace a storage incarnation. Caller holds Engine.Mu;
// point lookups below invoke neither providers nor callbacks under the leaf lock.
func (e *Engine) reconcileLogicalBuffersLocked() {
	// The template-copy lifecycle deliberately publishes a private catalog
	// before assigning Cluster. There is no shared cache to invalidate there.
	if e.Cluster == nil {
		return
	}
	cache := e.Cluster.buffercache
	if cache == nil {
		return
	}
	view := e.Catalog.Current()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key, entry := range cache.byKey {
		if key.database != 0 {
			if _, live := view.Database(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: key.database}); !live {
				cache.retireLocked(key, entry)
				continue
			}
		}
		if key.domain != view.Owner().Domain {
			continue
		}
		relation, exists := view.Relation(key.relation)
		current, eligible := logicalBufferIdentity(view, relation)
		if !exists || !eligible || current != key {
			cache.retireLocked(key, entry)
		}
	}
}
