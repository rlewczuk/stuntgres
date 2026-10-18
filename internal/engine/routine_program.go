package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Preparation records invocations, not granted rights. A graph can disappear
// after constant computation, but its original lifetime still fences subsequent
// admission after a wait. Values/providers are never replayed by readmit.
type routineAdmission struct {
	routine   *procedureDescriptor
	reference ids.ObjectRef
	consumer  *relationDescriptor
	body      routineBodyContext
	actor     authorization.PrincipalRef
}
type routineAdmissionProgram struct{ calls []routineAdmission }

func (p *routineAdmissionProgram) record(c *EvalContext, dependencies []expressionDependency, routine *procedureDescriptor) error {
	var ref ids.ObjectRef
	if c.routineReferences != nil {
		for _, candidate := range c.routineReferences {
			if candidate.Address == routine.Address() {
				ref = candidate
				break
			}
		}
	} else {
		if c.routineChecks != nil {
			dependencies = c.routineChecks
		}
		for _, candidate := range dependencies {
			if candidate.Ref.Address == routine.Address() {
				ref = candidate.Ref
				break
			}
		}
	}
	if ref.Address != (routine.Address()) {
		return fmt.Errorf("stuntgres: missing consumed routine lifetime")
	}
	auth, _, err := c.routineAdmissionAuthority()
	if err != nil {
		return err
	}
	for _, call := range p.calls {
		if call.reference == ref && call.consumer == c.routineConsumer && call.actor == auth.state.CurrentUser {
			return nil
		}
	}
	p.calls = append(p.calls, routineAdmission{routine: routine, reference: ref, consumer: c.routineConsumer, body: c.routineBodyContext, actor: auth.state.CurrentUser})
	return nil
}

func (p *routineAdmissionProgram) readmit(auth AuthorizationView) error {
	for _, call := range p.calls {
		current := auth
		current.state.CurrentUser = call.actor
		if err := current.roles.ValidatePrincipal(call.actor); err != nil {
			return err
		}
		dependency := [1]expressionDependency{{Ref: call.reference, Revision: call.routine.Revision()}}
		if err := admitRoutineDescriptor(current, dependency[:], call.routine, call.consumer); err != nil {
			return call.body.annotate(err)
		}
	}
	return nil
}
