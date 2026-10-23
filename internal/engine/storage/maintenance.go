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

// RecordVacuum records a VACUUM pass installed at completed: the scan facts of
// c, one more vacuum, no inserts since, and the frozen bound raised to c's
// horizon when c knows one.
func (m *Maintenance) RecordVacuum(c VacuumCandidate, completed time.Time) {
	m.Scanned, m.Live, m.Dead, m.LastVacuum = true, c.Live, c.Dead, completed
	m.Tuples = c.Live
	m.VacuumCount++
	m.InsertsSince = 0
	if c.Horizon != 0 && c.Horizon > m.FrozenXID {
		m.FrozenXID = c.Horizon
	}
}
