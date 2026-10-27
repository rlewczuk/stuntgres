package session

import "stuntgres/internal/engine/authorization"

// EffectiveUserInvocation is a local-userid change, not SET ROLE and not a
// security-restricted operation. Settings and transaction identity stay shared.
// Restricted records entry precedence: a later maintenance frame can override
// this actor without erasing the enclosing definer's prohibitions.
type EffectiveUserInvocation struct {
	Owner      authorization.PrincipalRef
	Parent     *EffectiveUserInvocation
	Restricted *RestrictedInvocation
}

func (op *Operation) EffectiveUserOwner() (authorization.PrincipalRef, bool) {
	if frame := op.EffectiveUser; frame != nil && frame.Restricted == op.Restricted {
		return frame.Owner, true
	}
	if op.Restricted != nil {
		return op.Restricted.Owner, true
	}
	return authorization.PrincipalRef{}, false
}
