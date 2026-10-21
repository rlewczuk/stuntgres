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
	Pending    map[*lock.Ticket]*exclusionProjection
	generation uint64
}

// installLocked publishes p as ticket's projection.
func (r *exclusionRegistry) InstallLocked(ticket *lock.Ticket, p *exclusionProjection) {
	if r.Pending == nil {
		r.Pending = make(map[*lock.Ticket]*exclusionProjection)
	}
	r.Pending[ticket] = p
	r.generation++
}

// projectionLocked returns a private copy of ticket's projection for the
// caller to change and install; an empty one when ticket has none.
func (r *exclusionRegistry) ProjectionLocked(ticket *lock.Ticket) *exclusionProjection {
	return cloneExclusionProjection(r.Pending[ticket])
}

// ownLocked returns ticket's published projection, or nil. Projections are
// immutable once installed; the caller only reads it.
func (r *exclusionRegistry) OwnLocked(ticket *lock.Ticket) *exclusionProjection {
	return r.Pending[ticket]
}

// removeLocked drops ticket's projection, if it has one.
func (r *exclusionRegistry) RemoveLocked(ticket *lock.Ticket) {
	if _, exists := r.Pending[ticket]; exists {
		delete(r.Pending, ticket)
		r.generation++
	}
}

// restoreLocked sets ticket's projection back to a savepoint's, dropping it
// when the savepoint had none.
func (r *exclusionRegistry) RestoreLocked(ticket *lock.Ticket, p *exclusionProjection) {
	if p == nil {
		delete(r.Pending, ticket)
	} else {
		if r.Pending == nil {
			r.Pending = make(map[*lock.Ticket]*exclusionProjection)
		}
		r.Pending[ticket] = p
	}
	r.generation++
}

// resetLocked drops every projection; publication replaced the catalog epoch.
func (r *exclusionRegistry) ResetLocked() {
	r.Pending = nil
	r.generation++
}

// scanLocked captures the generation and a copy of every projection for an
// exclusion scan.
func (r *exclusionRegistry) ScanLocked() (uint64, map[*lock.Ticket]*exclusionProjection) {
	return r.generation, maps.Clone(r.Pending)
}

// currentLocked reports whether no projection changed since a scan captured
// generation.
func (r *exclusionRegistry) CurrentLocked(generation uint64) bool {
	return r.generation == generation
}
