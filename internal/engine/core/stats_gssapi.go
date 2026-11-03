package core

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/pgcatalog"
)

// Engine.mu and transaction admission are held. Capture raw membership once in
// the transaction reader, then classify with this execution's coherent current
// authority. Neither a prepared binding nor the reader retains filtered rows.
func (e *Engine) captureGSSAPISessionsLocked(scope *StatementScope, auth AuthorizationView) ([]pgcatalog.GSSAPISession, error) {
	if _, err := auth.Roles.Descriptor(auth.State.CurrentUser); err != nil {
		return nil, err
	}
	statsRole, ok := auth.Roles.Lookup("pg_read_all_stats")
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing pg_read_all_stats role")
	}
	readAll, err := auth.Roles.InheritsRole(auth.State.CurrentUser, statsRole.Ref)
	if err != nil {
		return nil, err
	}
	_, activities := e.captureActivitiesLocked(scope, false)
	visible := make([]pgcatalog.GSSAPISession, 0, len(activities))
	for token, activity := range activities {
		if err := scope.Op.Context().Err(); err != nil {
			return nil, err
		}
		if err := auth.Roles.ValidatePrincipal(activity.Principal); err != nil {
			return nil, err
		}
		allowed := readAll
		if !allowed {
			// A retired login identity cannot be adopted by a same-name role.
			// Stats readers still see the real attached backend after DROP ROLE.
			if _, exists := auth.Roles.Find(activity.Principal); exists {
				allowed, err = auth.Roles.InheritsRole(auth.State.CurrentUser, activity.Principal)
				if err != nil {
					return nil, err
				}
			}
		}
		if !allowed {
			continue
		}
		id := token.ID()
		if id == 0 || id > math.MaxInt32 {
			return nil, fmt.Errorf("stuntgres: session identity is not a positive int4")
		}
		visible = append(visible, pgcatalog.GSSAPISession{PID: int32(id)})
	}
	return visible, nil
}
