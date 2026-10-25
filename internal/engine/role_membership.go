package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func membershipLocked(e *core.Engine, op *session.Operation, v core.AuthorizationView, s *parser.RoleMembership) (core.Result, error) {
	for {
		n, changed, notices, err := authorization.MembershipCandidate(op.Context(), v.Roles, v.State, s, op.Notices, op.NoticeThreshold <= session.LevelNotice, op.NoticeThreshold <= session.LevelWarning)
		if err != nil {
			op.Notices = notices
		}
		if err != nil {
			return core.Result{}, err
		}
		if scope := core.ScopeOfLease(op.Lease); scope != nil && !scope.Finished {
			e.Mu.Unlock()
			lockErr := acquireMembershipLocks(scope, v, s, &n)
			e.Mu.Lock()
			if lockErr != nil {
				op.Notices = notices
				return core.Result{}, lockErr
			}
			fresh, err := e.AuthorizationLocked(op)
			if err != nil {
				return core.Result{}, err
			}
			if fresh.RoleRecord != v.RoleRecord {
				v = fresh
				continue
			}
		}
		op.Notices = notices
		if !changed {
			return core.Result{}, nil
		}
		if v.RoleRecord.Revision == ^uint64(0) {
			return core.Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
		}
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := core.PublicationOperationLocked(e, op); err != nil {
			return core.Result{}, err
		}
		return core.Result{}, core.InstallOperationRolesLocked(e, op, &access.RoleRecord{Root: n})
	}
}
