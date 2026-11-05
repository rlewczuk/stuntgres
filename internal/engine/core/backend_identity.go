package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Engine.Mu pins current role/catalog authority and the shared detached activity
// cut. No peer admission, cumulative capture, manager waits or live-PID gate is
// involved. The projection borrows immutable roots after cluster unlock.
func (h *sessionInquiryHost) backendIdentityInput() (pgcatalog.Input, error) {
	if err := h.host().Context().Err(); err != nil {
		return pgcatalog.Input{}, err
	}
	s := h.host().scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return pgcatalog.Input{}, err
	}
	_, activities := e.captureActivitiesLocked(s, false)
	return pgcatalog.Input{Context: h.host().Context(), Catalog: auth.Catalog, Roles: auth.Roles, ActivityObserver: auth.State.CurrentUser, Activities: activities}, nil
}
func (h *sessionInquiryHost) BackendIdentityIDs(result *sqltype.Descriptor) ([]sqltype.Value, error) {
	in, err := h.backendIdentityInput()
	if err != nil {
		return nil, err
	}
	return pgcatalog.BackendIdentityIDs(in, result)
}
func (h *sessionInquiryHost) BackendIdentityValue(kind functions.BackendIdentityKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	in, err := h.backendIdentityInput()
	if err != nil {
		return sqltype.Value{}, err
	}
	return pgcatalog.BackendIdentityValue(in, kind, id, result)
}
