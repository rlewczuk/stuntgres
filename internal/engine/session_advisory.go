package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to lock.Manager.
type sessionAdvisory struct {
	ticket *lock.Ticket
	locks  *lock.Manager
}

func (a *sessionAdvisory) EndSession(token ids.SessionToken, _ EndReason) error {
	if a.ticket.Session() != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.locks.End(a.ticket)
	return nil
}
