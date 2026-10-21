package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

var _ partitionRecipeHost = (*statementHost)(nil)

func (h *statementHost) CommandIdentity() ids.CommandIdentity {
	return ids.CommandIdentity{Transaction: h.txRef, Command: h.command}
}

func (h *statementHost) PreparedDomainChecks() map[*catalog.Type]exec.DomainCheckProgram {
	return h.domains.preparedChecks
}

func (h *statementHost) ReferentialActor() (authorization.PrincipalRef, bool) {
	if h.foreignActionAuth == nil {
		return authorization.PrincipalRef{}, false
	}
	return h.foreignActionAuth.State.CurrentUser, true
}

// PartitionPrograms reads the partitions slot without allocating it.
func (h *statementHost) PartitionPrograms(identity partitionRoutineIdentity) []expr.Expr {
	return h.scope().partitionsOrZero().programs[identity]
}

func (h *statementHost) RecordPartitionPrograms(identity partitionRoutineIdentity, expressions []expr.Expr, admission exec.RoutineProgram) {
	slot := h.scope().partitions()
	if slot.programs == nil {
		slot.programs = make(map[partitionRoutineIdentity][]expr.Expr)
	}
	slot.programs[identity] = expressions
	h.routinePrograms = append(h.routinePrograms, commandRoutineProgram{admission: admission})
}
