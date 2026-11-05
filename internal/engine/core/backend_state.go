package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func (h *sessionInquiryHost) BackendStateValue(kind functions.BackendStateKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := h.host().Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	s := h.host().scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	waits := kind == functions.BackendStateWaitEvent || kind == functions.BackendStateWaitEventType
	live, activities := e.captureActivitiesLocked(s, waits)
	in := pgcatalog.Input{Context: h.host().Context(), Catalog: auth.Catalog, Roles: auth.Roles, ActivityObserver: auth.State.CurrentUser, Activities: activities}
	e.Mu.Unlock()
	// Manager metadata is never entered under the engine/provider lock.
	if waits {
		e.overlayActivityWaits(live, activities)
	}
	return pgcatalog.BackendStateValue(in, kind, id, result)
}
