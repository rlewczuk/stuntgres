package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
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
	authority *exec.Authority
	// Only index captures collect. Operation supplies live restricted/proconfig
	// settings at Open; its transaction owner serializes the pending Writer.
	op  *session.Operation
	ref ids.ObjectRef
}

func (r *logicalBufferRead) Open() {
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
	if s.Op == nil || s.Op.Session == nil || s.Engine() != e || EngineOf(s.Op.Session) != e || s.Op.Session.SessionToken != r.owner || s.Active() != nil {
		return
	}
	valid, resident := r.openLocked()
	if valid {
		v := statistics.StatementCounters{SharedRead: 1}
		if resident {
			v.SharedRead, v.SharedHit = 0, 1
		}
		s.recordStatementCounters(v)
	}
}

// Engine.Mu is held. The cache returns the atomic resident-before-touch outcome;
// the capture mutex is released before invoking settings or statistics owners.
func (r *logicalBufferRead) openLocked() (valid, resident bool) {
	valid, resident = r.openCacheLocked()
	if !valid {
		return false, false
	}
	// Cache leaf is released before settings or provider authority is entered.
	if r.op != nil && !r.op.Released {
		_, _, policy := r.op.EffectiveSettings().StatsConfiguration()
		PayloadOf(r.op.Session).statsWriter.IndexBlock(&r.engine.Statistics, statistics.Key{Object: r.ref, Category: statistics.Relation}, policy, resident)
	}
	return true, resident
}

// Engine.Mu is held. This cache-only half is safe for a qualified worker: no
// operation, lexical scope, settings or cumulative Writer is touched.
func (r *logicalBufferRead) openCacheLocked() (valid, resident bool) {
	e := r.engine
	// Unpublished template runtimes have no cluster. A bridge from a retired
	// runtime cannot acquire a shared cache owner before attachment.
	if e.Cluster == nil {
		return false, false
	}
	view := e.Catalog.Current()
	database, exists := view.Database(e.Catalog.DatabaseAddress())
	if !exists || e.Cluster.Targets[database.Name()] != e || view.Epoch() != r.key.epoch {
		return false, false
	}
	relation, ok := view.Relation(r.key.relation)
	if !ok {
		return false, false
	}
	key, eligible := logicalBufferIdentity(view, relation)
	if !eligible || key != r.key.logicalBufferIncarnation {
		return false, false
	}
	r.mu.Lock()
	cache := e.logicalBuffersLocked()
	resident = false
	if r.opened && r.entry != nil {
		resident = cache.touch(r.entry)
	}
	if !resident {
		r.entry, resident = cache.open(r.key, r.owner)
	}
	r.opened, r.cache = true, cache
	r.mu.Unlock()
	return true, resident
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
func logicalBufferIdentity(view catalog.Read, relation *catalog.Relation) (logicalBufferIncarnation, bool) {
	if relation == nil || view.Owner() == nil {
		return logicalBufferIncarnation{}, false
	}
	namespace, ok := view.Namespace(relation.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return logicalBufferIncarnation{}, false
	}
	node := relation.EffectiveStorageLocator(view)
	if node == 0 {
		return logicalBufferIncarnation{}, false
	}
	key := logicalBufferIncarnation{domain: view.Owner().Domain, epoch: view.Epoch(), relation: relation.Address(), node: node, space: catalog.DefaultTablespaceOID, database: view.Owner().DatabaseAddress().ObjectID}
	if catalog.SharedRelation(relation) {
		key.space, key.database = catalog.GlobalTablespaceOID, 0
	}
	return key, true
}
func (e *Engine) logicalBufferReadKeyLocked(scope *StatementScope, view catalog.Read, address ids.ObjectAddress) (logicalBufferKey, bool) {
	// CREATE DATABASE's private replacement target is not an execution/cache
	// owner until the database is published and attached to its real cluster.
	if e.Cluster == nil {
		return logicalBufferKey{}, false
	}
	relation, ok := view.Relation(address)
	if !ok {
		return logicalBufferKey{}, false
	}
	// Private heap rewrites retain committed locators until publication. An
	// attached index shares its parent's rewrite intent, not its journal key.
	owner := address
	if relation.SQLIndex != nil {
		owner = relation.SQLIndex.Parent
	}
	if journal := scope.PendingCatalog(); journal != nil && journal.Storage[owner].Rewritten {
		return logicalBufferKey{}, false
	}
	key, eligible := logicalBufferIdentity(view, relation)
	if !eligible {
		return logicalBufferKey{}, false
	}
	committed, ok := e.Catalog.Current().Relation(address)
	if !ok {
		return logicalBufferKey{}, false
	}
	incumbent, eligible := logicalBufferIdentity(e.Catalog.Current(), committed)
	if !eligible || incumbent != key {
		return logicalBufferKey{}, false
	}
	return logicalBufferKey{logicalBufferIncarnation: key}, true
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
		if !exists || !eligible || current != key.logicalBufferIncarnation {
			cache.retireLocked(key, entry)
		}
	}
}
