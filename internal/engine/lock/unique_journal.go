package lock

// UniqueJournal binds the manager to one statement's ticket for the ON
// CONFLICT unique-key journal: checkpoints of held key contributions and the
// delta of newly granted claims. Every method takes only the manager mutex
// and never waits.
type UniqueJournal struct {
	m *Manager
	t *Ticket
}

// UniqueJournal returns t's unique-key journal.
func (m *Manager) UniqueJournal(t *Ticket) UniqueJournal { return UniqueJournal{m: m, t: t} }

// Checkpoint is CheckpointUnique: it records the held key contributions,
// reusing held as scratch.
func (j UniqueJournal) Checkpoint(held Checkpoint) Checkpoint { return j.m.CheckpointUnique(j.t, held) }

// Restore is RestoreUnique: it masks each key hold back to held.
func (j UniqueJournal) Restore(held Checkpoint) { j.m.RestoreUnique(j.t, held) }

// BeginDelta starts journaling newly granted unique claims.
func (j UniqueJournal) BeginDelta() { j.m.BeginUniqueDelta(j.t) }

// EndDelta stops journaling and forgets the delta.
func (j UniqueJournal) EndDelta() { j.m.EndUniqueDelta(j.t) }

// RestoreDelta drops the journaled claims and empties the delta.
func (j UniqueJournal) RestoreDelta() { j.m.RestoreUniqueDelta(j.t) }
