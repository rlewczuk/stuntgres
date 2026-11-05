package core

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

// Routine admission supplies current EXECUTE. Cluster publication is serialized
// by Engine.Mu; provider mutexes remain leaves and peer writers are never used.
func (h *statementHost) StatsResetShared(kind functions.StatsResetSharedKind, argument sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.Op == nil || s.Op.Session == nil || result == nil || result.ID() != sqltype.Void || kind.Name() == "" || argument.Type() == nil || argument.Type().ID() != kind.ArgumentType() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid shared statistics reset authority")
	}
	if err := h.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if _, err := s.RoutineAuthorizationLocked(); err != nil {
		return sqltype.Value{}, err
	}
	var target statistics.SharedKind
	var name string
	if kind != functions.ResetBackendStats && !argument.IsNull() {
		var err error
		name, err = argument.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if kind == functions.ResetShared {
			var valid bool
			target, valid = statistics.SharedTarget(name)
			if !valid {
				return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`unrecognized reset target: "%s"`, name), Hint: `Target must be "archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", or "wal".`}
			}
		}
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if kind == functions.ResetBackendStats {
		if argument.IsNull() {
			return sqltype.Null(result), nil
		}
		pid, err := argument.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		e.resetBackendPIDLocked(pid, at)
	} else {
		if kind == functions.ResetShared && e.Cluster != nil {
			if argument.IsNull() || target == statistics.Archiver {
				e.Cluster.ArchiverResetAt = at
			}
			if argument.IsNull() || target == statistics.BGWriter {
				e.Cluster.BGWriterResetAt = at
			}
			if argument.IsNull() || target == statistics.Checkpointer {
				e.Cluster.CheckpointerResetAt = at
			}
			if argument.IsNull() || target == statistics.SharedWAL {
				e.Cluster.WALResetAt = at
			}
		}
		reset := func(p *statistics.Provider) {
			if kind == functions.ResetSLRU {
				if argument.IsNull() {
					p.ResetShared(statistics.SLRU, at)
				} else {
					p.ResetSLRU(statistics.SLRUTarget(name), at)
				}
				return
			}
			if argument.IsNull() {
				p.ResetAllShared(at)
			} else {
				p.ResetShared(target, at)
			}
		}
		reset(&e.Statistics)
		if e.Cluster != nil {
			for _, peer := range e.Cluster.Targets {
				if peer != e {
					reset(&peer.Statistics)
				}
			}
			if retained := e.Cluster.DefaultTarget; retained != e && e.Cluster.Targets[retained.DatabaseName] != retained {
				reset(&retained.Statistics)
			}
		}
	}
	return sqltype.VoidValue(result), nil
}

// Registry ownership is released before lifecycle ownership, matching the
// existing signal classifier. Full tokens, not PID-shaped object keys, reset
// every retained partition of a live backend while its lifetime is pinned.
func (e *Engine) resetBackendPIDLocked(pid int64, at time.Time) {
	if pid <= 0 {
		return
	}
	find := func(target *Engine) (*session.Session, ids.SessionToken) {
		target.sessionMu.Lock()
		defer target.sessionMu.Unlock()
		for token, s := range target.sessions {
			if uint64(token.ID()) == uint64(pid) {
				return s, token
			}
		}
		return nil, ids.SessionToken{}
	}
	var candidate *session.Session
	var token ids.SessionToken
	candidate, token = find(e)
	if candidate == nil && e.Cluster != nil {
		for _, peer := range e.Cluster.Targets {
			if peer != e {
				candidate, token = find(peer)
				if candidate != nil {
					break
				}
			}
		}
	}
	if candidate == nil {
		return
	}
	candidate.Mu.Lock()
	defer candidate.Mu.Unlock()
	if candidate.State != session.Live {
		return
	}
	e.Statistics.ResetBackend(token, at)
	if e.Cluster != nil {
		for _, peer := range e.Cluster.Targets {
			if peer != e {
				peer.Statistics.ResetBackend(token, at)
			}
		}
		if retained := e.Cluster.DefaultTarget; retained != e && e.Cluster.Targets[retained.DatabaseName] != retained {
			retained.Statistics.ResetBackend(token, at)
		}
	}
}
