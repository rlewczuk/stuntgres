package core

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// These are invocation state, not lexical SQL scratch or rollback snapshots.
// Ordinary routines and event triggers borrow the owner without entering it.
func (s *StatementScope) enterTrigger() { s.activeTriggerDepth++ }
func (s *StatementScope) leaveTrigger() { s.activeTriggerDepth-- }

func (h *statementHost) EnterForeignKeyTrigger() { h.scope().enterTrigger() }
func (h *statementHost) LeaveForeignKeyTrigger() { h.scope().leaveTrigger() }

func (h *statementHost) TriggerDepthValue(result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || result == nil || result.ID() != sqltype.Int4 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid trigger depth authority")
	}
	return sqltype.Integer(result, int64(s.activeTriggerDepth))
}
