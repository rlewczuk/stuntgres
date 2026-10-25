package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) membershipLocked(op *session.Operation, v AuthorizationView, s *parser.RoleMembership) (Result, error) {
	for {
		n, changed, notices, err := authorization.MembershipCandidate(op.Context(), v.Roles, v.State, s, op.Notices, op.NoticeThreshold <= session.LevelNotice, op.NoticeThreshold <= session.LevelWarning)
		if err != nil {
			op.Notices = notices
		}
		if err != nil {
			return Result{}, err
		}
		if scope := ScopeOfLease(op.Lease); scope != nil && !scope.Finished {
			e.Mu.Unlock()
			lockErr := scope.acquireMembershipLocks(v, s, &n)
			e.Mu.Lock()
			if lockErr != nil {
				op.Notices = notices
				return Result{}, lockErr
			}
			fresh, err := e.AuthorizationLocked(op)
			if err != nil {
				return Result{}, err
			}
			if fresh.RoleRecord != v.RoleRecord {
				v = fresh
				continue
			}
		}
		op.Notices = notices
		if !changed {
			return Result{}, nil
		}
		if v.RoleRecord.Revision == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
		}
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := PublicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		return Result{}, e.installOperationRolesLocked(op, &access.RoleRecord{Root: n})
	}
}
