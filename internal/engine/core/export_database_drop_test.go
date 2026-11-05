package core

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// DatabaseAdmissionClaim exposes the actual first half of StartSession, so a
// composed test can retain the consumed token before its initializer binds it.
func DatabaseAdmissionClaim(a *DatabaseAdmission, e *Engine, start session.SessionStart) error {
	return a.claim(e, start.ID)
}

// EngineStartClaimedSession is StartSession's unchanged continuation after claim.
func EngineStartClaimedSession(e *Engine, ctx context.Context, a *DatabaseAdmission, start session.SessionStart) (*session.Session, error) {
	return e.startSession(ctx, start, a.principal, a, false)
}

// DatabaseAdmissionOwnership observes the canonical ledger, not a parallel owner.
func DatabaseAdmissionOwnership(a *DatabaseAdmission) (uint8, bool, *session.Session, *lock.Ticket, int, int) {
	a.cluster.AdmissionMu.Lock()
	defer a.cluster.AdmissionMu.Unlock()
	return a.state, a.revoked, a.owner, a.ticket, a.cluster.admissions[a.target], a.cluster.principalAdmissions[a.principal]
}

// EngineStartPrivateSession uses the exact private initializer behind
// StartRestoreSession with a real defaults provider. It deliberately does not
// simulate StartRestoreSession's outer trusted-replay origin association.
func EngineStartPrivateSession(e *Engine, ctx context.Context, principal authorization.PrincipalRef, defaults settings.SessionDefaultProvider) (*session.Session, error) {
	return e.startSession(ctx, session.SessionStart{Defaults: defaults}, principal, nil, true)
}

// EnginePrivateStarts snapshots the actual unpublished private-owner cohort.
func EnginePrivateStarts(e *Engine) []*session.Session {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	var starts []*session.Session
	for s := range e.privateStarts {
		starts = append(starts, s)
	}
	return starts
}
