package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

func (h *sessionInquiryHost) BackendActivity(argument sqltype.Value, shape *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := h.host().Context().Err(); err != nil {
		return nil, err
	}
	s := h.host().scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	live, activities := e.captureActivitiesLocked(s, true)
	in := pgcatalog.Input{Context: h.host().Context(), Catalog: auth.Catalog, Roles: auth.Roles, ActivityObserver: auth.State.CurrentUser, Activities: activities}
	e.Mu.Unlock()
	e.overlayActivityWaits(live, activities)
	pid := int64(-1)
	if !argument.IsNull() {
		pid, err = argument.Integer()
		if err != nil {
			return nil, err
		}
	}
	return pgcatalog.BackendActivity(in, pid, shape)
}

// Engine.Mu pins cluster membership. Registry locks are released before taking
// lifecycle ownership. The candidate's full physical token must also belong to
// the retained raw activity cut before any cumulative cache is consulted.
func (h *sessionInquiryHost) backendFact(pid int64) (statistics.BackendFact, string, bool, error) {
	if err := h.host().Context().Err(); err != nil {
		return statistics.BackendFact{}, "", false, err
	}
	s := h.host().scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if pid <= 0 {
		return statistics.BackendFact{}, "", false, nil
	}
	var candidate *session.Session
	var token ids.SessionToken
	var owner *Engine
	find := func(target *Engine) {
		target.sessionMu.Lock()
		defer target.sessionMu.Unlock()
		for t, session := range target.sessions {
			if int64(t.ID()) == pid {
				if p := PayloadOf(session); p != nil && p.statsPublic {
					candidate, token, owner = session, t, target
				}
				return
			}
		}
	}
	find(e)
	if candidate == nil && e.Cluster != nil {
		for _, target := range e.Cluster.Targets {
			if target != e {
				find(target)
			}
			if candidate != nil {
				break
			}
		}
	}
	if candidate == nil {
		return statistics.BackendFact{}, "", false, nil
	}
	candidate.Mu.Lock()
	defer candidate.Mu.Unlock()
	if candidate.State != session.Live {
		return statistics.BackendFact{}, "", false, nil
	}
	_, activities := e.captureActivitiesLocked(s, false)
	a, present := activities[token]
	if !present || !pgcatalog.BackendStatisticsKind(a.BackendType) {
		return statistics.BackendFact{}, "", false, nil
	}
	current, live := e.activityProvider().LiveActivity(token)
	if !live || current.Incarnation == 0 || current.Incarnation != a.Incarnation || !current.BackendStart.Equal(a.BackendStart) {
		return statistics.BackendFact{}, "", false, nil
	}
	reader := statsReader(s.Op.Session, s.Op.EffectiveSettings())
	captureDatabaseStats(e, reader)
	fact, present := reader.ReadBackend(&owner.Statistics, statistics.BackendIdentity{Token: token, Incarnation: current.Incarnation}, time.Now().UTC().Truncate(time.Microsecond))
	return fact, a.BackendType, present, nil
}
func (h *sessionInquiryHost) BackendIO(pid int64, shape *sqltype.Descriptor) ([]sqltype.Value, error) {
	fact, kind, present, err := h.backendFact(pid)
	if err != nil || !present {
		return nil, err
	}
	var types [20]*sqltype.Descriptor
	for i := range types {
		types[i] = shape.CompositeMember(i).Usage.Type
	}
	rows, err := pgcatalog.PhysicalIORows(h.host().Context(), types, kind, fact.ResetAt)
	if err != nil {
		return nil, err
	}
	values := make([]sqltype.Value, len(rows))
	for i, row := range rows {
		values[i], err = sqltype.OwnedCompositeValue(shape, row)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}
func (h *sessionInquiryHost) BackendWAL(pid int64, shape *sqltype.Descriptor) (sqltype.Value, error) {
	fact, _, present, err := h.backendFact(pid)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !present {
		return sqltype.Null(shape), nil
	}
	var types [5]*sqltype.Descriptor
	for i := range types {
		types[i] = shape.CompositeMember(i).Usage.Type
	}
	row, err := pgcatalog.PhysicalWALRow(h.host().Context(), types, fact.ResetAt)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedCompositeValue(shape, row)
}
