package session

import (
	"sync"
	"time"

	"stuntgres/internal/engine/functions"
)

// Session-owned, nontransactional clock. Only the clock update holds mu;
// argument evaluation, calendar arithmetic and cryptographic entropy never do.
// Both v7 overloads share this state; catalog replacement does not reset it.
type uuidClock struct {
	mu       sync.Mutex
	previous int64
}

func (s *uuidClock) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previous = functions.UUIDAscendingTime(s.previous, time.Now().UnixNano(), functions.UUIDClockStep())
	return s.previous
}
