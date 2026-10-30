package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Engine.Mu owns every transition. A stage owns provisional exclusion until
// its immutable candidate is transferred to a statement journal.
type replicationOriginReservation struct {
	Ref      ids.TxRef
	Address  ids.ObjectAddress
	Stage    *CatalogStage
	Prepared bool
	// Zero denotes a standalone claim; subscription claims retain this scope
	// through journal, savepoint and prepared ownership transfers.
	SubscriptionDatabase ids.ObjectAddress
}

type replicationOriginOccupancy [1024]uint64

func (b *replicationOriginOccupancy) add(id ids.OID) {
	if id > 0 && id < 65535 {
		b[id/64] |= uint64(1) << (id % 64)
	}
}

func (b *replicationOriginOccupancy) allocate(ctx context.Context) (ids.OID, error) {
	for id := ids.OID(1); id < 65535; id++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if b[id/64]&(uint64(1)<<(id%64)) == 0 {
			b.add(id)
			return id, nil
		}
	}
	return 0, SQLError("54000", "could not find free replication origin ID")
}

// Replacements disregard selected published definitions, but never pending
// claims: their old transaction may still own recoverable journal backing.
func (e *Engine) replicationOriginOccupancyLocked(replacingSelected bool) replicationOriginOccupancy {
	var occupied replicationOriginOccupancy
	for _, d := range e.Catalog.Current().ReplicationOrigins() {
		sub, _ := e.Catalog.Current().Get(d.Subscription).(*catalog.Subscription)
		if !replacingSelected || sub != nil && sub.Database != e.Catalog.DatabaseAddress() {
			occupied.add(d.Address().ObjectID)
		}
	}
	if e.Cluster != nil {
		for id := range e.Cluster.replicationOrigins {
			occupied.add(id)
		}
	}
	return occupied
}

func (h DDLHost) AllocateReplicationOrigin() (ids.OID, error) {
	occupied := h.E.replicationOriginOccupancyLocked(false)
	for _, d := range h.StageRead().ReplicationOrigins() {
		occupied.add(d.Address().ObjectID)
	}
	id, err := occupied.allocate(h.Context())
	if err != nil {
		return 0, err
	}
	stage := h.CatalogStage
	ref := stage.InitialTx
	if stage.statement != nil {
		ref = stage.statement.TxRef
	}
	if h.E.Cluster.replicationOrigins == nil {
		h.E.Cluster.replicationOrigins = make(map[ids.OID]replicationOriginReservation)
	}
	h.E.Cluster.replicationOrigins[id] = replicationOriginReservation{Ref: ref, Address: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: id}, Stage: stage, SubscriptionDatabase: h.E.Catalog.DatabaseAddress()}
	stage.replicationOrigins = append(stage.replicationOrigins, id)
	return id, nil
}

func (h DDLHost) AbandonReplicationOrigin() {
	h.E.releaseStageReplicationOriginsLocked(h.CatalogStage)
}

func (e *Engine) releaseStageReplicationOriginsLocked(stage *CatalogStage) {
	for _, id := range stage.replicationOrigins {
		if reservation, ok := e.Cluster.replicationOrigins[id]; ok && reservation.Stage == stage {
			delete(e.Cluster.replicationOrigins, id)
		}
	}
	stage.replicationOrigins = nil
}

func (e *Engine) transferReplicationOriginsLocked(stage *CatalogStage, ref ids.TxRef) {
	for _, id := range stage.replicationOrigins {
		if reservation, ok := e.Cluster.replicationOrigins[id]; ok && reservation.Stage == stage {
			reservation.Stage, reservation.Ref = nil, ref
			e.Cluster.replicationOrigins[id] = reservation
		}
	}
	stage.replicationOrigins = nil
}

func journalOwnsReplicationOrigin(journal *session.Journal, id ids.OID, address ids.ObjectAddress) bool {
	if journal == nil {
		return false
	}
	d, ok := journal.Descriptors[address].(*catalog.ReplicationOrigin)
	return ok && d.Address().ObjectID == id
}

// The caller owns t.Mu/admission; never inspect another transaction's journal.
func (e *Engine) reconcileReplicationOriginsLocked(t *session.TransactionOwner) {
	e.reconcileStatementReplicationOriginsLocked(t, nil)
}

func (e *Engine) reconcileStatementReplicationOriginsLocked(t *session.TransactionOwner, statement *session.Journal) {
	for id, reservation := range e.Cluster.replicationOrigins {
		if reservation.Ref != t.Ref || reservation.Prepared || reservation.Stage != nil {
			continue
		}
		retained := journalOwnsReplicationOrigin(t.Catalog, id, reservation.Address) || journalOwnsReplicationOrigin(statement, id, reservation.Address)
		for _, mark := range t.Savepoints {
			retained = retained || journalOwnsReplicationOrigin(mark.Catalog, id, reservation.Address) || journalOwnsReplicationOrigin(mark.StatementCatalog, id, reservation.Address)
		}
		if !retained {
			delete(e.Cluster.replicationOrigins, id)
		}
	}
}

func (e *Engine) releaseReplicationOriginsLocked(ref ids.TxRef) {
	for id, reservation := range e.Cluster.replicationOrigins {
		if reservation.Ref == ref && !reservation.Prepared {
			delete(e.Cluster.replicationOrigins, id)
		}
	}
}

func releaseReplicationOrigins(t *session.TransactionOwner) {
	e := EngineOf(t.Session)
	e.Mu.Lock()
	e.releaseReplicationOriginsLocked(t.Ref)
	e.Mu.Unlock()
}

func (e *Engine) prepareReplicationOriginsLocked(ref ids.TxRef, prepared bool) {
	for id, reservation := range e.Cluster.replicationOrigins {
		if reservation.Ref == ref {
			reservation.Prepared = prepared
			e.Cluster.replicationOrigins[id] = reservation
		}
	}
}

func (e *Engine) retireReplacedReplicationOriginsLocked() {
	// CREATE DATABASE prepares an unregistered runtime before attaching it to
	// the cluster. It owns no pending exclusions to retire at this boundary.
	if e.Cluster == nil {
		return
	}
	for id, reservation := range e.Cluster.replicationOrigins {
		if reservation.Ref.Domain() == e.Catalog.Domain && reservation.Ref.Epoch() != e.Catalog.State.Epoch() {
			delete(e.Cluster.replicationOrigins, id)
		}
	}
}

func (e *Engine) validateReplacementReplicationOriginsLocked(next *catalog.State) error {
	read := e.Catalog.Read(next)
	if err := catalog.ValidateReplicationOrigins(read); err != nil {
		return err
	}
	for _, d := range read.ReplicationOrigins() {
		sub, _ := read.Get(d.Subscription).(*catalog.Subscription)
		if sub != nil && sub.Database != e.Catalog.DatabaseAddress() {
			continue
		}
		if e.Cluster != nil {
			if _, exists := e.Cluster.replicationOrigins[d.Address().ObjectID]; exists {
				return ids.ErrCatalogReplaced
			}
		}
	}
	// Retained peers must still be their exact current immutable descriptors.
	for address, d := range e.Catalog.Current().Subscriptions() {
		if d.Database == e.Catalog.DatabaseAddress() {
			continue
		}
		if read.Get(address) != d {
			return ids.ErrCatalogReplaced
		}
		for originAddress, origin := range e.Catalog.Current().ReplicationOrigins() {
			if origin.Subscription == address && read.Get(originAddress) != origin {
				return ids.ErrCatalogReplaced
			}
		}
	}
	return nil
}
