package core

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Engine.Mu owns the context and the cluster's index into its physical payload.
// Numeric selection survives ordinary catalog rollback; native replacement alone
// invalidates selected scopes. No catalog graph or transaction journal is retained.
type replicationOriginSession struct {
	id       ids.OID
	token    ids.SessionToken
	scope    ids.ObjectAddress
	domain   *ids.Domain
	epoch    ids.CatalogEpoch
	position uint64
	stamp    int64         // Backend-global PG microseconds, retained without selection; 0 is unset.
	released chan struct{} // Lazily allocated only by a genuine internal-drop waiter.
}

func originSlotFull(id ids.OID) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400",
		Message: fmt.Sprintf("could not find free replication state slot for replication origin with ID %d", id),
		Hint:    `Increase "max_active_replication_origins" and try again.`,
	}
}

func (e *Engine) originActiveErrorLocked(id ids.OID, drop bool) error {
	if e.Cluster == nil {
		return nil
	}
	if owner := e.Cluster.replicationOriginOwners[id]; owner != nil {
		if drop {
			return SQLError("55006", fmt.Sprintf("could not drop replication origin with ID %d, in use by PID %d", id, owner.token.ID()))
		}
		return SQLError("55006", fmt.Sprintf("replication origin with ID %d is already active for PID %d", id, owner.token.ID()))
	}
	return nil
}

func (e *Engine) releaseOriginSessionLocked(s *session.Session, token ids.SessionToken) {
	p := PayloadOf(s)
	if p == nil || p.origin.token != token {
		return
	}
	c := &p.origin
	if e.Cluster.replicationOriginOwners[c.id] == c {
		delete(e.Cluster.replicationOriginOwners, c.id)
	}
	c.clearLocked()
}

func (e *Engine) setupOriginSessionLocked(s *session.Session, read catalog.Read, d *catalog.ReplicationOrigin, capacity int) error {
	c := &PayloadOf(s).origin
	if c.id != 0 {
		return SQLError("55000", "cannot setup replication origin when one is already setup")
	}
	id := d.Address().ObjectID
	if err := e.originActiveErrorLocked(id, false); err != nil {
		return err
	}
	if _, present := e.Cluster.replicationOriginProgress[id]; !present && len(e.Cluster.replicationOriginProgress) >= capacity {
		return originSlotFull(id)
	}
	if e.Cluster.replicationOriginProgress == nil {
		e.Cluster.replicationOriginProgress = make(map[ids.OID]uint64)
	}
	if e.Cluster.replicationOriginOwners == nil {
		e.Cluster.replicationOriginOwners = make(map[ids.OID]*replicationOriginSession)
	}
	scope := ids.ObjectAddress{}
	if reservation, ok := e.Cluster.replicationOrigins[id]; ok {
		scope = reservation.SubscriptionDatabase
	} else if sub, ok := read.Get(d.Subscription).(*catalog.Subscription); ok {
		scope = sub.Database
	}
	if e.Cluster.replicationOriginProgressGeneration == nil {
		e.Cluster.replicationOriginProgressGeneration = make(map[ids.ObjectAddress]uint64)
	}
	if _, present := e.Cluster.replicationOriginProgressGeneration[scope]; !present {
		e.Cluster.replicationOriginProgressGeneration[scope] = 0
	}
	// Acquiring selection does not reset the backend-global LSN/timestamp.
	c.id, c.token, c.scope = id, s.Token(), scope
	c.domain, c.epoch = e.Catalog.Domain, e.Catalog.State.Epoch()
	e.Cluster.replicationOriginOwners[id] = c
	if _, present := e.Cluster.replicationOriginProgress[id]; !present {
		e.Cluster.replicationOriginProgress[id] = 0
		e.bumpOriginProgressScopeLocked(scope)
	}
	return nil
}

// Called after strict operands/live EXECUTE admission, before named-state dispatch.
func (h *statementHost) replicationOriginSessionCall(kind functions.ReplicationOriginKind, values [2]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.Op.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	c := &PayloadOf(s.Op.Session).origin
	if kind == functions.ReplicationOriginSessionIsSetup {
		return sqltype.Boolean(result, c.id != 0), nil
	}
	if kind == functions.ReplicationOriginXactReset {
		c.position, c.stamp = 0, 0
		return sqltype.VoidValue(result), nil
	}
	if c.id == 0 {
		return sqltype.Value{}, SQLError("55000", "no replication origin is configured")
	}
	switch kind {
	case functions.ReplicationOriginSessionReset:
		e.releaseOriginSessionLocked(s.Op.Session, s.Op.Session.Token())
		return sqltype.VoidValue(result), nil
	case functions.ReplicationOriginXactSetup:
		position, err := values[0].PGLSN()
		if err != nil {
			return sqltype.Value{}, err
		}
		stamp, err := values[1].Timestamp()
		if err != nil {
			return sqltype.Value{}, err
		}
		c.position, c.stamp = position, stamp
		return sqltype.VoidValue(result), nil
	case functions.ReplicationOriginSessionProgress:
		flush, err := values[0].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
		if flush {
			return sqltype.Value{}, SQLError("0A000", "pg_replication_origin_session_progress with flush=true requires durable WAL flushing, which is unsupported in embedded mode")
		}
		if position := e.Cluster.replicationOriginProgress[c.id]; position != 0 {
			return sqltype.PGLSNValue(result, position)
		}
		return sqltype.Null(result), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid replication origin session operation")
}

// The caller fences the transaction's actual completion and owns Engine.Mu.
// Assignment is inspected before history consumes it. PREPARE/subabort advance
// without consuming the detached/outer identity; abort never records history.
func (e *Engine) advanceOriginSessionLocked(ref ids.TxRef, actor *session.Session) *replicationOriginSession {
	if actor == nil || e.Visibility.Entries[ref].AssignedXID == 0 {
		return nil
	}
	p := PayloadOf(actor)
	if p == nil {
		return nil
	}
	c := &p.origin
	if c.id == 0 || c.token != actor.Token() || e.Cluster.replicationOriginOwners[c.id] != c {
		return nil
	}
	if old := e.Cluster.replicationOriginProgress[c.id]; c.position > old {
		e.Cluster.replicationOriginProgress[c.id] = c.position
	}
	e.bumpOriginProgressScopeLocked(c.scope)
	return c
}

const postgresEpochUnixMicros int64 = 946684800000000

// recordOriginCommitLocked records the commit timestamp and origin of an
// XID-bearing commit, which ends its XID assignment, and returns the XID with
// them.
func (e *Engine) recordOriginCommitLocked(ref ids.TxRef, actor *session.Session) (logicalCommitIdentity, bool) {
	xid := e.Visibility.Entries[ref].AssignedXID
	if xid == 0 {
		return logicalCommitIdentity{}, false
	}
	origin := uint32(0)
	var stamp int64
	if c := e.advanceOriginSessionLocked(ref, actor); c != nil {
		origin = uint32(c.id)
		if c.stamp == 0 {
			c.stamp = time.Now().UnixMicro() - postgresEpochUnixMicros
		}
		stamp = c.stamp
	} else {
		stamp = time.Now().UnixMicro() - postgresEpochUnixMicros
		if actor != nil {
			if p := PayloadOf(actor); p != nil {
				p.origin.stamp = stamp
			}
		}
	}
	e.Visibility.RecordCommitTimestampLocked(ref, stamp, origin)
	return logicalCommitIdentity{xid: xid, stamp: stamp, origin: origin}, true
}

func (e *Engine) invalidateReplacedOriginSessionsLocked() {
	if e.Cluster == nil {
		return
	}
	for id, c := range e.Cluster.replicationOriginOwners {
		if !e.retainedPeerOriginSessionLocked(c) {
			delete(e.Cluster.replicationOriginOwners, id)
			c.clearLocked()
		}
	}
}

// Replacement shares this exact classification for ID exclusion, slot union,
// collision preflight and physical-context retention, including active orphans.
func (e *Engine) retainedPeerOriginSessionLocked(c *replicationOriginSession) bool {
	if c == nil || c.id == 0 || e.Cluster.replicationOriginOwners[c.id] != c || c.token.Domain() != c.domain || !session.TokenAttached(c.token) || c.scope == (ids.ObjectAddress{}) || c.scope == e.Catalog.DatabaseAddress() {
		return false
	}
	for store := range e.Cluster.Catalogs {
		if store.Domain == c.domain && store.DatabaseAddress() == c.scope && store.State.Epoch() == c.epoch {
			return true
		}
	}
	return false
}

// Only marks installed in the actual child stack are subtransactions. Atomic
// statement snapshots must not manufacture an abort boundary. Engine.Mu and
// the current owner's admission are held; no canonical identity is consumed.
func (e *Engine) abortOriginSubtransactionLocked(t *session.TransactionOwner, serial uint64) {
	for i := range t.Savepoints {
		if t.Savepoints[i].Serial == serial {
			if t.Savepoints[i].Assigned {
				e.advanceOriginSessionLocked(t.Ref, t.Session)
			}
			for j := i; j < len(t.Savepoints); j++ {
				t.Savepoints[j].Assigned = false
			}
			return
		}
	}
}

func (c *replicationOriginSession) clearLocked() {
	if c.released != nil {
		close(c.released)
	}
	*c = replicationOriginSession{}
}
