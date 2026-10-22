package storage

import "time"

// Maintenance is coherent logical metadata, guarded by Engine.mu. It never
// represents physical pages, WAL, MultiXacts or process-local attempted work.
type Maintenance struct {
	FrozenXID                                              uint64
	Scanned                                                bool
	Live, Dead                                             int64
	Tuples                                                 int64 // Estimate from the last successful main scan, not DML deltas.
	Inserts, Updates, Deletes, Modifications, InsertsSince int64
	VacuumCount                                            int64
	LastVacuum                                             time.Time
}

// RecordBirth keeps an already initialized bound justified by a newly published
// unfrozen version. A zero bound stays unknown until the engine initializes it
// against the canonical active-publisher cutoff and the complete current root.
func (m *Maintenance) RecordBirth(fullXID uint64) {
	if fullXID < m.FrozenXID {
		m.FrozenXID = fullXID
	}
}
