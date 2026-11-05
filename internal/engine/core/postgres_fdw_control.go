package core

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
)

// Only an existing server/mapping successor or removal can stale an owned
// connection or pending Open. New objects and other catalog classes cannot.
func postgresFDWIdentityChanged(before, after catalog.Read, address ids.ObjectAddress) bool {
	if address.ClassID != ids.ForeignServerClassID && address.ClassID != ids.ForeignMappingClassID {
		return false
	}
	old, exists := before.Foreign(address)
	if !exists {
		return false
	}
	next, exists := after.Foreign(address)
	return !exists || old.Revision() != next.Revision()
}

func postgresFDWStageChanged(before, after catalog.Read, stage *CatalogStage) bool {
	for address := range stage.Descriptors() {
		if postgresFDWIdentityChanged(before, after, address) {
			return true
		}
	}
	for address := range stage.Removals() {
		if postgresFDWIdentityChanged(before, after, address) {
			return true
		}
	}
	return false
}

func postgresFDWJournalChanged(before, after catalog.Read, changes *session.Journal) bool {
	if changes == nil {
		return false
	}
	for address := range changes.Descriptors {
		if postgresFDWIdentityChanged(before, after, address) {
			return true
		}
	}
	return false
}

// Engine.Mu precedes Session.Mu -> Owner.mu. sessionMu is released before any
// Session.Mu acquisition, matching registration/terminal lock order. No external
// Cancel/Close/join runs here. locked names an already-held Session.Mu.
func (e *Engine) invalidatePostgresLocked(read catalog.Read, replacement bool, locked *session.Session) []remote.PostgresCleanup {
	e.sessionMu.Lock()
	physical := make([]*session.Session, 0, len(e.sessions))
	for _, s := range e.sessions {
		physical = append(physical, s)
	}
	e.sessionMu.Unlock()
	var cleanup []remote.PostgresCleanup
	for _, s := range physical {
		if s != locked {
			s.Mu.Lock()
		}
		payload := PayloadOf(s)
		if payload != nil && payload.externalSQL != nil {
			detached := payload.externalSQL.InvalidatePostgres(read, replacement)
			if !detached.Empty() {
				cleanup = append(cleanup, detached)
			}
		}
		if s != locked {
			s.Mu.Unlock()
		}
	}
	return cleanup
}
func closePostgresCleanup(cleanup []remote.PostgresCleanup) error {
	for _, c := range cleanup {
		c.Cancel()
	}
	var failures []error
	for _, c := range cleanup {
		if err := c.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Private successful statements invalidate only their issuing physical owner.
// Commit publication separately invalidates peers. A failed stage/statement and
// a later ROLLBACK never invalidate another session's committed connection.
func (s *StatementScope) invalidatePostgresPrivate() error {
	e := s.Engine()
	e.Mu.Lock()
	read := e.PrivateCatalogLocked(s.Owner.Catalog)
	physical := s.Op.Session
	physical.Mu.Lock()
	var cleanup remote.PostgresCleanup
	if payload := PayloadOf(physical); payload != nil && payload.externalSQL != nil {
		cleanup = payload.externalSQL.InvalidatePostgres(read, false)
	}
	physical.Mu.Unlock()
	e.Mu.Unlock()
	return cleanup.Close()
}
