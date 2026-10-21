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
func (h *statementHost) FunctionUUIDTime() int64 { return h.op.Session.UUID.Now() }
func (h *statementHost) FunctionUUIDV1(multicast bool) ([16]byte, error) {
	return h.op.Session.UUIDV1(h.Context(), multicast)
}
