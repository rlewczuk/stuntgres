package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func appendBlockingPid(values []sqltype.Value, typ *sqltype.Descriptor, token ids.SessionToken, prepared bool, domain *ids.Domain) ([]sqltype.Value, error) {
	pid, visible, err := lock.BlockingPid(token, prepared, domain)
	if err != nil {
		return nil, err
	}
	if !visible {
		return values, nil
	}
	value, err := sqltype.Integer(typ, int64(pid))
	if err != nil {
		return nil, err
	}
	return append(values, value), nil
}

// blockingPidValues projects the lock manager's blocking PIDs of one session.
func (e *Engine) blockingPidValues(pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	pids, err := e.Locks.BlockingPids(e.Catalog.Domain, pid)
	if err != nil || len(pids) == 0 {
		return nil, err
	}
	values := make([]sqltype.Value, len(pids))
	for i, pid := range pids {
		if values[i], err = sqltype.Integer(typ, int64(pid)); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// Caller holds Engine.mu. These flags describe an unresolved acquisition,
// unlike possible/ready alone, which also exist for nondeferrable readers.
func (e *Engine) safeSnapshotBlockingPidsLocked(pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	var values []sqltype.Value
	for _, writer := range e.Visibility.SafeSnapshotBlockersLocked(e.Catalog.Domain, e.Catalog.State.Epoch(), e.Catalog.Domain.Session(pid)) {
		var err error
		values, err = appendBlockingPid(values, typ, writer.Session, writer.Prepared, e.Catalog.Domain)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}
