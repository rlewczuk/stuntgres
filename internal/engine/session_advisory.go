package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to lock.Manager.
type sessionAdvisory struct {
	Ticket *lock.Ticket
	Locks  *lock.Manager
}

func (a *sessionAdvisory) EndSession(token ids.SessionToken, _ EndReason) error {
	if a.Ticket.Session() != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.Locks.End(a.Ticket)
	return nil
}
