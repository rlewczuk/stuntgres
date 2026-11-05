package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) SessionSignalValue(c *exec.Context, kind functions.SessionSignalKind, pid, timeout int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.Op == nil || s.Op.Session == nil || result == nil || result.ID() != sqltype.Bool || kind.Name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid session signal authority")
	}
	timeout = session.SignalTimeout(timeout)
	if kind == functions.SignalTerminate && timeout < 0 {
		return sqltype.Value{}, SQLError("22003", `"timeout" must not be negative`)
	}
	e := s.Engine()
	e.Mu.Lock()
	target := e.sessionSignalTargetLocked(pid)
	if target == nil {
		e.Mu.Unlock()
		s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{
			Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000",
			Message: fmt.Sprintf("PID %d is not a PostgreSQL backend process", pid),
		})
		return sqltype.Boolean(result, false), nil
	}
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	actor := expressionPrincipal(c)
	err = sessionSignalPermission(auth.Roles, actor, target.CountedAuthenticated, kind)
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	target.Mu.Lock()
	if kind == functions.SignalCancel {
		target.CancelQueryLocked()
	} else {
		target.TerminateLocked()
	}
	target.Mu.Unlock()
	e.Mu.Unlock()
	if kind == functions.SignalCancel || timeout == 0 {
		return sqltype.Boolean(result, true), nil
	}
	completed, err := target.WaitTermination(c.Context, timeout)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !completed {
		unit := "milliseconds"
		if timeout == 1 {
			unit = "millisecond"
		}
		s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{
			Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000",
			Message: fmt.Sprintf("backend with PID %d did not terminate within %d %s", pid, timeout, unit),
		})
	}
	return sqltype.Boolean(result, completed), nil
}

func sessionSignalDenied(kind functions.SessionSignalKind, superuser bool) error {
	message := "permission denied to cancel query"
	detail := `Only roles with privileges of the role whose query is being canceled or with privileges of the "pg_signal_backend" role may cancel this query.`
	if superuser {
		detail = "Only roles with the SUPERUSER attribute may cancel queries of roles with the SUPERUSER attribute."
	}
	if kind == functions.SignalTerminate {
		message = "permission denied to terminate process"
		detail = `Only roles with privileges of the role whose process is being terminated or with privileges of the "pg_signal_backend" role may terminate this process.`
		if superuser {
			detail = "Only roles with the SUPERUSER attribute may terminate processes of roles with the SUPERUSER attribute."
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message, Detail: detail}
}

// The victim is its immutable authenticated physical principal, not SET ROLE.
// FORCE uses this same evaluator for every reservation before signaling any.
func sessionSignalPermission(roles authorization.Read, actor, victim authorization.PrincipalRef, kind functions.SessionSignalKind) error {
	caller, err := roles.Descriptor(actor)
	if err != nil {
		return err
	}
	// A physical owner can outlive its role tuple. Its original principal must
	// still belong to this authority; absence is not permission to adopt a
	// same-named replacement, nor to accept a foreign or zero principal.
	if err := roles.ValidatePrincipal(victim); err != nil {
		return err
	}
	owner, exists := roles.Find(victim)
	if exists && owner.Superuser && !caller.Superuser {
		return sessionSignalDenied(kind, true)
	}
	if caller.Superuser {
		return nil
	}
	allowed := false
	if exists {
		allowed, err = roles.InheritsRole(actor, victim)
		if err != nil {
			return err
		}
	}
	if !allowed {
		if signal, ok := roles.Lookup("pg_signal_backend"); ok {
			allowed, err = roles.InheritsRole(actor, signal.Ref)
		}
	}
	if err != nil {
		return err
	}
	if !allowed {
		return sessionSignalDenied(kind, owner.Superuser)
	}
	return nil
}

// Engine.Mu protects the cluster frontier. Release each registry mutex before
// Session.Mu (startup and unregister publish in the opposite direction).
// Ending remains addressable until actual cleanup unregisters it.
func (e *Engine) sessionSignalTargetLocked(pid int64) *session.Session {
	if pid <= 0 {
		return nil
	}
	for _, runtime := range e.Cluster.Targets {
		runtime.sessionMu.Lock()
		var target *session.Session
		for token, candidate := range runtime.sessions {
			if uint64(token.ID()) == uint64(pid) {
				target = candidate
				break
			}
		}
		runtime.sessionMu.Unlock()
		if target != nil {
			return target
		}
	}
	return nil
}
