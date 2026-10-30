package core

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ServerSignalValue(kind functions.ServerSignalKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope == nil || scope.Op == nil || scope.Op.Session == nil || result == nil || result.ID() != sqltype.Bool {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid server signal authority")
	}
	var mechanism string
	switch kind {
	case functions.SignalReload:
		mechanism = "configuration-file reload"
	case functions.SignalRotate:
		mechanism = "log-file rotation"
	case functions.SignalMemory:
		if !scope.Engine().serverSignalPIDLive(pid) {
			scope.Op.EmitNotice(session.LevelWarning, sqlstate.Error{
				Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000",
				Message: fmt.Sprintf("PID %d is not a PostgreSQL server process", pid),
			})
			return sqltype.Boolean(result, false), nil
		}
		mechanism = "backend memory-context logging"
	default:
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid server signal kind")
	}
	return sqltype.Value{}, &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000",
		Message: kind.Name()+" "+mechanism+" is unsupported in embedded mode",
	}
}

// The cluster mutex owns target publication. Registry locks are released before
// taking a session lock: session startup holds its session lock while publishing.
// Classification linearizes at the state observation, without sending a signal.
func (e *Engine) serverSignalPIDLive(pid int64) bool {
	if pid <= 0 { return false }
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for _, runtime := range e.Cluster.Targets {
		var candidate *session.Session
		runtime.sessionMu.Lock()
		for token, s := range runtime.sessions {
			if uint64(token.ID()) == uint64(pid) {
				candidate = s
				break
			}
		}
		runtime.sessionMu.Unlock()
		if candidate != nil {
			candidate.Mu.Lock()
			live := candidate.State == session.Live
			candidate.Mu.Unlock()
			return live
		}
	}
	return false
}
