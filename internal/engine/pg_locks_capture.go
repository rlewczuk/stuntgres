package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
)

// Engine.mu is held. The retained node's exact ref supplies original backend
// association even after commit; no session lookup or live ticket is needed.
func (e *Engine) capturePGPredicateLocksLocked() ([]lock.Record, error) {
	var records []lock.Record
	for _, holder := range e.Visibility.PredicateLocksLocked(e.Catalog.State.Epoch()) {
		owner, visible, err := lock.PublicOwner(e.Catalog.Domain, holder.Ref, holder.Prepared)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		for address := range holder.Reads {
			records = append(records, lock.Record{Kind: lock.PredicateRecord, Owner: owner, Object: uint32(address.ObjectID)})
		}
	}
	return records, nil
}

// Consumes Engine.mu. Only scalar SSI/database facts are captured before the
// manager is entered. Captured sources own values; prepared handles own recipes.
func (e *Engine) capturePGLocksLocked(ctx context.Context, ref ids.ObjectRef, d *catalog.Relation) (*exec.Source, error) {
	database, ok := e.Catalog.State.Database(e.Catalog.Owner.DatabaseAddress())
	if !ok {
		e.Mu.Unlock()
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	databaseOID, epoch := uint32(database.Address().ObjectID), e.Catalog.State.Epoch()
	predicates, err := e.capturePGPredicateLocksLocked()
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	records, err := e.Locks.PGLocks(e.Catalog.Domain, epoch)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	current := e.Catalog.State.Epoch() == epoch
	e.Mu.Unlock()
	if !current {
		return nil, ids.ErrCatalogReplaced
	}
	rows, err := pgcatalog.LockRows(ctx, d, databaseOID, records, predicates)
	if err != nil {
		return nil, err
	}
	return &exec.Source{Ref: ref, Anonymous: rows}, nil
}
