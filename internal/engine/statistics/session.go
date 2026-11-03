package statistics

import "time"

// SessionUsage owns monotonic interval frontiers, serialized by its physical
// owner's admission. Draining consumes elapsed time, never samples old spans.
type SessionUsage struct {
	at                                  time.Time
	active, idleTransaction, activities bool
}

func (s *SessionUsage) Begin(at time.Time) { s.at = at }
func (s *SessionUsage) Transition(at time.Time, active, idleTransaction, activities bool) DatabaseFact {
	var f DatabaseFact
	if !s.at.IsZero() {
		elapsed := at.Sub(s.at)
		f.SessionTime = elapsed
		if s.activities {
			if s.active {
				f.ActiveTime = elapsed
			} else if s.idleTransaction {
				f.IdleTransactionTime = elapsed
			}
		}
	}
	s.at, s.active, s.idleTransaction, s.activities = at, active, idleTransaction, activities
	return f
}
