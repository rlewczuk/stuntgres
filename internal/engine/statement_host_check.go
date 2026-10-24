package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/table"
)

var _ table.CheckRecipeHost = (*statementHost)(nil)

func (h *statementHost) CheckPrograms() map[*catalog.Relation]exec.CheckProgram {
	return h.checkPrograms
}

// Preparation publishes only complete phases. The stable program pointer also
// retains calls consumed later by lazy truth, in the existing readmission order.
func (h *statementHost) RecordCheckProgram(owner *catalog.Relation, program exec.CheckProgram, phase exec.CheckPhase) {
	if h.checkPrograms == nil {
		h.checkPrograms = make(map[*catalog.Relation]exec.CheckProgram)
	}
	h.checkPrograms[owner] = program
	h.routinePrograms = append(h.routinePrograms, commandRoutineProgram{consumer: owner, ri: phase.RI, admission: phase.Admission})
}
