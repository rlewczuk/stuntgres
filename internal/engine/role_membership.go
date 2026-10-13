package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

func (e *Engine) membershipLocked(op *Operation, v AuthorizationView, s *parser.RoleMembership) (Result, error) {
	n, changed, notices, err := authorization.MembershipCandidate(op.Context(), v.roles, v.state, s, op.notices, op.noticeThreshold <= levelNotice, op.noticeThreshold <= levelWarning)
	op.notices = notices
	if err != nil {
		return Result{}, err
	}
	if !changed {
		return Result{}, nil
	}
	if v.roleRecord.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.installOperationRolesLocked(op, &roleCatalog{root: n, revision: v.roleRecord.revision + 1})
	return Result{}, nil
}
