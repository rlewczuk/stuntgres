package exec

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Preparation records invocations, not granted rights. A graph can disappear
// after constant computation, but its original lifetime still fences subsequent
// admission after a wait. Values/providers are never replayed by readmit.
type RoutineCall struct {
	Routine   *catalog.Procedure
	Reference ids.ObjectRef
	Consumer  *catalog.Relation
	body      RoutineBodyContext
	Actor     authorization.PrincipalRef
}

type RoutineProgram struct{ Calls []RoutineCall }

func (p *RoutineProgram) Record(c *Context, dependencies []catalog.ExpressionDependency, routine *catalog.Procedure) error {
	var ref ids.ObjectRef
	if c.RoutineReferences != nil {
		for _, candidate := range c.RoutineReferences {
			if candidate.Address == routine.Address() {
				ref = candidate
				break
			}
		}
	} else {
		if c.RoutineChecks != nil {
			dependencies = c.RoutineChecks
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
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	for _, call := range p.Calls {
		if call.Reference == ref && call.Consumer == c.RoutineConsumer && call.Actor == auth.State.CurrentUser {
			return nil
		}
	}
	p.Calls = append(p.Calls, RoutineCall{Routine: routine, Reference: ref, Consumer: c.RoutineConsumer, body: c.RoutineBodyContext, Actor: auth.State.CurrentUser})
	return nil
}

func (p *RoutineProgram) Readmit(auth RoutineAccess) error {
	for _, call := range p.Calls {
		current := auth
		current.State.CurrentUser = call.Actor
		if err := current.Roles.ValidatePrincipal(call.Actor); err != nil {
			return err
		}
		dependency := [1]catalog.ExpressionDependency{{Ref: call.Reference, Revision: call.Routine.Revision()}}
		if err := AdmitRoutineDescriptor(current, dependency[:], call.Routine, call.Consumer); err != nil {
			return call.body.Annotate(err)
		}
	}
	return nil
}

// A new trusted caller phase may borrow phase-independent cooked values only
// after admitting the exact original consumed graph under its own actual actor.
func (p *RoutineProgram) InitializePhase(original *RoutineProgram, auth RoutineAccess) error {
	for _, call := range original.Calls {
		dependency := [1]catalog.ExpressionDependency{{Ref: call.Reference, Revision: call.Routine.Revision()}}
		if err := AdmitRoutineDescriptor(auth, dependency[:], call.Routine, call.Consumer); err != nil {
			return call.body.Annotate(err)
		}
		call.Actor = auth.State.CurrentUser
		p.Calls = append(p.Calls, call)
	}
	return nil
}

func (p *RoutineProgram) Include(other *RoutineProgram) {
	for _, call := range other.Calls {
		found := false
		for _, old := range p.Calls {
			if old.Reference == call.Reference && old.Consumer == call.Consumer && old.Actor == call.Actor {
				found = true
				break
			}
		}
		if !found {
			p.Calls = append(p.Calls, call)
		}
	}
}
