package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

func (e *Engine) membershipLocked(op *Operation, v AuthorizationView, s *parser.RoleMembership) (Result, error) {
	n, changed, notices, err := authorization.MembershipCandidate(op.Context(), v.Roles, v.State, s, op.notices, op.noticeThreshold <= levelNotice, op.noticeThreshold <= levelWarning)
	op.notices = notices
	if err != nil {
		return Result{}, err
	}
	if !changed {
		return Result{}, nil
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.installOperationRolesLocked(op, &access.RoleRecord{Root: n, Revision: v.RoleRecord.Revision + 1})
	return Result{}, nil
}
