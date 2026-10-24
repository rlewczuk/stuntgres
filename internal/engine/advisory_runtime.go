package engine

import (
	"errors"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) Advisory(c *exec.Context, action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s.op == nil || s.op.Session == nil || s.op.Session.Advisory.Ticket == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing advisory session scope")
	}
	ticket := s.op.Session.Advisory.Ticket
	if action == functions.AdvisoryUnlockAll {
		if err := s.engine().locks.UnlockAdvisoryAll(ticket); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.VoidValue(result), nil
	}
	ref := lock.Resource{Database: s.engine().catalog.Domain, Kind: lock.AdvisoryBigintResource, High: uint32(uint64(first) >> 32), Low: uint32(first)}
	if pair {
		ref = lock.Resource{Database: s.engine().catalog.Domain, Kind: lock.AdvisoryPairResource, High: uint32(first), Low: uint32(second)}
	}
	lockMode := lock.AdvisoryExclusive
	if mode == functions.AdvisoryShare {
		lockMode = lock.AdvisoryShare
	}
	if action == functions.AdvisoryUnlock {
		unlocked, err := s.engine().locks.UnlockAdvisory(ticket, ref, lockMode)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !unlocked {
			message := "you don't own a lock of type ExclusiveLock"
			if mode == functions.AdvisoryShare {
				message = "you don't own a lock of type ShareLock"
			}
			s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message})
		}
		return sqltype.Boolean(result, unlocked), nil
	}
	try := action == functions.AdvisoryTry
	acquired, err := s.acquireAdvisory(c.Context, ref, lockMode, lifetime, try)
	if err != nil {
		return sqltype.Value{}, err
	}
	if try {
		return sqltype.Boolean(result, acquired), nil
	}
	return sqltype.VoidValue(result), nil
}
