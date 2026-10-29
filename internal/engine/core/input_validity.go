package core

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) CheckTypeInput(c *exec.Context, value, typeName string) (*sqlstate.Error, error) {
	if err := h.Context().Err(); err != nil {
		return nil, err
	}
	if h.Finished {
		return nil, fmt.Errorf("stuntgres: ended type-input authority")
	}
	auth := h.RoutineAuth
	if current := RoutineAuthorityView(c); current != nil {
		auth = *current
	}
	resolver, err := CaptureRegtypeInput(auth, h.Op)
	if err != nil {
		return nil, err
	}
	usage, err := resolver.ResolveInputUsage(h.Context(), typeName)
	if err != nil {
		return nil, err
	}
	input := c.Input
	input.ACLItem = h.scope().Engine().CaptureACLItemInput(auth, h.Op)
	if err := h.scope().Engine().CaptureTypeDiagnosticInput(auth, h.Op, &input, usage.Type); err != nil {
		return nil, err
	}
	if err := h.scope().Engine().CaptureElementInput(auth, h.Op, &input); err != nil {
		return nil, err
	}
	input.DomainChecks = exec.InputDomainChecks(c, domainCatalog(c))
	// Domain CHECK evaluation reads the execution input environment, rather than
	// the value-input copy. Lend it this conversion's coherent naming authority
	// and restore the caller's environment on every success or failure path.
	previous := c.Input
	c.Input = input
	defer func() { c.Input = previous }()
	_, err = usage.Type.InputWithEnvironment(h.Context(), value, usage.Typmod, input)
	if err == nil {
		return nil, nil
	}
	// Classify the direct producer error, not its unwrapped diagnostic. In
	// particular an exception inside a domain CHECK must remain hard.
	if !sqltype.InputSoftError(err) {
		return nil, err
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return nil, err
	}
	copied := *diagnostic
	return &copied, nil
}
