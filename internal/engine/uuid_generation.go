package engine

import (
	"fmt"
)

func (h *statementHost) FunctionUUIDReady() error {
	scope := h.scope()
	if scope.op == nil || scope.op.Session == nil {
		return fmt.Errorf("stuntgres: missing UUID session scope")
	}
	return nil
}
func (h *statementHost) FunctionUUIDTime() int64 { return h.op.Session.UUID.now() }
