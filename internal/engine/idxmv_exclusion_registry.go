package engine

import (
	"maps"

	"stuntgres/internal/engine/lock"
)

// exclusionRegistry holds the dirty exclusion projections of every open lock
// owner and their generation. It has no mutex: Engine.mu guards it, and every
// method requires the caller to hold Engine.mu. Each change to a projection
// advances the generation, so an exclusion scan captured earlier fails
// currentLocked and is retaken.
type exclusionRegistry struct {
	pending    map[*lock.Ticket]*exclusionProjection
	generation uint64
}

// installLocked publishes p as ticket's projection.
func (r *exclusionRegistry) installLocked(ticket *lock.Ticket, p *exclusionProjection) {
	if r.pending == nil {
		r.pending = make(map[*lock.Ticket]*exclusionProjection)
	}
	r.pending[ticket] = p
	r.generation++
}

// projectionLocked returns a private copy of ticket's projection for the
// caller to change and install; an empty one when ticket has none.
func (r *exclusionRegistry) projectionLocked(ticket *lock.Ticket) *exclusionProjection {
	return cloneExclusionProjection(r.pending[ticket])
}

// ownLocked returns ticket's published projection, or nil. Projections are
// immutable once installed; the caller only reads it.
func (r *exclusionRegistry) ownLocked(ticket *lock.Ticket) *exclusionProjection {
	return r.pending[ticket]
}

// removeLocked drops ticket's projection, if it has one.
func (r *exclusionRegistry) removeLocked(ticket *lock.Ticket) {
	if _, exists := r.pending[ticket]; exists {
		delete(r.pending, ticket)
		r.generation++
	}
}

// restoreLocked sets ticket's projection back to a savepoint's, dropping it
// when the savepoint had none.
func (r *exclusionRegistry) restoreLocked(ticket *lock.Ticket, p *exclusionProjection) {
	if p == nil {
		delete(r.pending, ticket)
	} else {
		if r.pending == nil {
			r.pending = make(map[*lock.Ticket]*exclusionProjection)
		}
		r.pending[ticket] = p
	}
	r.generation++
}

// resetLocked drops every projection; publication replaced the catalog epoch.
func (r *exclusionRegistry) resetLocked() {
	r.pending = nil
	r.generation++
}

// scanLocked captures the generation and a copy of every projection for an
// exclusion scan.
func (r *exclusionRegistry) scanLocked() (uint64, map[*lock.Ticket]*exclusionProjection) {
	return r.generation, maps.Clone(r.pending)
}

// currentLocked reports whether no projection changed since a scan captured
// generation.
func (r *exclusionRegistry) currentLocked(generation uint64) bool {
	return r.generation == generation
}
