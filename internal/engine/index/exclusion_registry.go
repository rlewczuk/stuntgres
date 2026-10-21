package index

import (
	"maps"

	"stuntgres/internal/engine/lock"
)

// ExclusionRegistry holds the dirty exclusion projections of every open lock
// owner and their generation. It has no mutex: Engine.mu guards it, and every
// method requires the caller to hold Engine.mu. Each change to a projection
// advances the generation, so an exclusion scan captured earlier fails
// currentLocked and is retaken.
type ExclusionRegistry struct {
	Pending    map[*lock.Ticket]*ExclusionProjection
	generation uint64
}

// InstallLocked publishes p as ticket's projection.
func (r *ExclusionRegistry) InstallLocked(ticket *lock.Ticket, p *ExclusionProjection) {
	if r.Pending == nil {
		r.Pending = make(map[*lock.Ticket]*ExclusionProjection)
	}
	r.Pending[ticket] = p
	r.generation++
}

// ProjectionLocked returns a private copy of ticket's projection for the
// caller to change and install; an empty one when ticket has none.
func (r *ExclusionRegistry) ProjectionLocked(ticket *lock.Ticket) *ExclusionProjection {
	return cloneExclusionProjection(r.Pending[ticket])
}

// OwnLocked returns ticket's published projection, or nil. Projections are
// immutable once installed; the caller only reads it.
func (r *ExclusionRegistry) OwnLocked(ticket *lock.Ticket) *ExclusionProjection {
	return r.Pending[ticket]
}

// RemoveLocked drops ticket's projection, if it has one.
func (r *ExclusionRegistry) RemoveLocked(ticket *lock.Ticket) {
	if _, exists := r.Pending[ticket]; exists {
		delete(r.Pending, ticket)
		r.generation++
	}
}

// RestoreLocked sets ticket's projection back to a savepoint's, dropping it
// when the savepoint had none.
func (r *ExclusionRegistry) RestoreLocked(ticket *lock.Ticket, p *ExclusionProjection) {
	if p == nil {
		delete(r.Pending, ticket)
	} else {
		if r.Pending == nil {
			r.Pending = make(map[*lock.Ticket]*ExclusionProjection)
		}
		r.Pending[ticket] = p
	}
	r.generation++
}

// ResetLocked drops every projection; publication replaced the catalog epoch.
func (r *ExclusionRegistry) ResetLocked() {
	r.Pending = nil
	r.generation++
}

// ScanLocked captures the generation and a copy of every projection for an
// exclusion scan.
func (r *ExclusionRegistry) ScanLocked() (uint64, map[*lock.Ticket]*ExclusionProjection) {
	return r.generation, maps.Clone(r.Pending)
}

// CurrentLocked reports whether no projection changed since a scan captured
// generation.
func (r *ExclusionRegistry) CurrentLocked(generation uint64) bool {
	return r.generation == generation
}
