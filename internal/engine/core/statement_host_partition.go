package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
)

var _ partition.RecipeHost = (*statementHost)(nil)

func (h *statementHost) CommandIdentity() ids.CommandIdentity {
	return ids.CommandIdentity{Transaction: h.TxRef, Command: h.CommandID}
}

func (h *statementHost) PreparedDomainChecks() map[*catalog.Type]exec.DomainCheckProgram {
	return h.Domains.PreparedChecks
}

func (h *statementHost) ReferentialActor() (authorization.PrincipalRef, bool) {
	if h.ForeignActionAuth == nil {
		return authorization.PrincipalRef{}, false
	}
	return h.ForeignActionAuth.State.CurrentUser, true
}

// PartitionPrograms reads the partitions slot without allocating it.
func (h *statementHost) PartitionPrograms(identity partition.RoutineIdentity) []expr.Expr {
	return h.scope().PartitionsOrZero().Programs[identity]
}

func (h *statementHost) RecordPartitionPrograms(identity partition.RoutineIdentity, expressions []expr.Expr, admission exec.RoutineProgram) {
	slot := h.scope().partitions()
	if slot.Programs == nil {
		slot.Programs = make(map[partition.RoutineIdentity][]expr.Expr)
	}
	slot.Programs[identity] = expressions
	h.routinePrograms = append(h.routinePrograms, commandRoutineProgram{admission: &admission})
}
