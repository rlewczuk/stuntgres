package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// fireTableRewrite borrows exactly this command's scratch at the existing
// unlocked physical-work boundary. Nested utilities own another command.
func (s *StatementScope) fireTableRewrite(target ids.OID, reason int32) error {
	command := s.EventCommand
	if command == nil {
		return nil
	}
	previousOID, previousReason := command.RewriteOID, command.RewriteReason
	command.RewriteOID, command.RewriteReason = target, reason
	defer func() { command.RewriteOID, command.RewriteReason = previousOID, previousReason }()
	return s.fireEventTrigger("table_rewrite")
}

func (h *statementHost) EventTriggerRewriteValue(oid bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.EventCommand == nil || s.EventCommand.Phase != "table_rewrite" || s.EventCommand.RewriteOID == 0 || s.EventCommand.RewriteReason == 0 {
		return sqltype.Value{}, functions.EventTriggerRewriteContextError(oid)
	}
	if err := s.Op.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	if oid {
		return sqltype.OIDValue(result, uint32(s.EventCommand.RewriteOID))
	}
	return sqltype.Integer(result, int64(s.EventCommand.RewriteReason))
}
