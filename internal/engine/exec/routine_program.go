package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Preparation records invocations, not granted rights. A graph can disappear
// after constant computation, but its original lifetime still fences subsequent
// admission after a wait. Values/providers are never replayed by readmit.
type RoutineCall struct {
	Routine          *catalog.Procedure
	Reference        ids.ObjectRef
	Consumer         *catalog.Relation
	Recipe           expr.RoutineRecipeReference
	body             RoutineBodyContext
	Actor            authorization.PrincipalRef
	SupportAggregate *catalog.Procedure   // Only top-level support EXECUTE uses its current owner.
	followIdentity   bool                 // Original checked recipe, not original SQL source.
	defaultCall      *acceptedDefaultCall // Consumed occurrence evidence, never a grant.
	nativeSource     nativeRoutineSource  // Immutable source provenance only; never a runtime owner/receipt.
}

type RoutineProgram struct{ Calls []RoutineCall }

// Readmission never replays an effect or re-enters a past trusted subject.
// Fresh caller initialization is distinct from validating an already consumed
// graph after a wait/publication boundary.
type RoutineReadmissionPurpose uint8

const (
	ConsumedGraphRevalidation RoutineReadmissionPurpose = iota + 1
	CallerInitialization
)

func (p *RoutineProgram) Record(c *Context, dependencies []catalog.ExpressionDependency, routine *catalog.Procedure) error {
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	return p.RecordActor(c, dependencies, routine, auth.State.CurrentUser, nil)
}

func routineCapturePurpose(aggregate *catalog.Procedure) catalog.DependencyPurpose {
	if aggregate != nil {
		return catalog.AggregateSupportDependency
	}
	return catalog.DefinitionDependency
}

// RecordActor records support EXECUTE under its owner without changing the
// invocation/body role of the evaluation context.
func (p *RoutineProgram) RecordActor(c *Context, dependencies []catalog.ExpressionDependency, routine *catalog.Procedure, actor authorization.PrincipalRef, aggregate *catalog.Procedure) error {
	followIdentity := c.RoutineChecks != nil || c.RoutineReferences != nil
	var recipe expr.RoutineRecipeReference
	if c.RoutineGraft {
		recipe = c.RoutineGraftSource
	}
	var ref ids.ObjectRef
	var nativeSource nativeRoutineSource
	if c.nativeBody.parent.Site.Routine != nil {
		auth, _, err := RoutineInvocationAuthority(c)
		if err != nil {
			return err
		}
		var handled bool
		ref, handled, err = nativeBodyRoutineLifetime(c, auth, routine, routineCapturePurpose(aggregate))
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("stuntgres: missing consumed native body source")
		}
		nativeSource = nativeSourceOf(c.nativeBody.parent.Site)
	} else if c.RoutineReferences != nil {
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
			if candidate.Ref.Address == routine.Address() && candidate.Revision == routine.Revision() && candidate.Purpose == routineCapturePurpose(aggregate) {
				ref = candidate.Ref
				break
			}
		}
	}
	if ref.Address != (routine.Address()) {
		return fmt.Errorf("stuntgres: missing consumed routine lifetime")
	}
	var bounded *acceptedDefaultCall
	if c.defaultCall != nil {
		if aggregate != nil || c.defaultCall.original.Procedure != routine {
			return ids.ErrCatalogReplaced
		}
		if _, err := c.acceptedDefaultInvocation(c.defaultCall); err != nil {
			return err
		}
		bounded = c.defaultCall
	}
	for _, call := range p.Calls {
		if call.Reference == ref && call.Routine == routine && call.Recipe == recipe && call.Consumer == c.RoutineConsumer && call.Actor == actor && call.SupportAggregate == aggregate && call.followIdentity == followIdentity && call.defaultCall == bounded && call.body == c.RoutineBodyContext && call.nativeSource == nativeSource {
			return nil
		}
	}
	p.Calls = append(p.Calls, RoutineCall{Routine: routine, Reference: ref, Consumer: c.RoutineConsumer, Recipe: recipe, body: c.RoutineBodyContext, Actor: actor, SupportAggregate: aggregate, followIdentity: followIdentity, defaultCall: bounded, nativeSource: nativeSource})
	return nil
}

func (call RoutineCall) readmitNativeSource(auth RoutineAccess) (bool, error) {
	if call.nativeSource.Routine == nil {
		return false, nil
	}
	ref, err := nativeSourceRoutineLifetime(auth, call.nativeSource, call.Routine)
	if err == nil && ref != call.Reference {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true})
	}
	return true, err
}

func (p *RoutineProgram) Readmit(auth RoutineAccess, phase RoutineReadmissionPurpose) error {
	if phase != ConsumedGraphRevalidation {
		return fmt.Errorf("stuntgres: invalid consumed routine readmission purpose")
	}
	auth.Sepgsql = access.SepgsqlAuthority{}
	for _, call := range p.Calls {
		current := auth
		current.State.CurrentUser = call.Actor
		if call.SupportAggregate != nil {
			aggregate, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, call.SupportAggregate)
			if err != nil {
				return err
			}
			current.State.CurrentUser = aggregate.Owner
		}
		if err := current.Roles.ValidatePrincipal(current.State.CurrentUser); err != nil {
			return err
		}
		if handled, err := call.readmitNativeSource(current); handled {
			if err != nil {
				return call.body.Annotate(err)
			}
			continue
		}
		if call.defaultCall != nil {
			if _, err := call.defaultCall.artifact.validate(current, AcceptedDefaultCall{node: call.defaultCall}); err != nil {
				return call.body.Annotate(err)
			}
			continue
		}
		purpose := routineCapturePurpose(call.SupportAggregate)
		dependency := [1]catalog.ExpressionDependency{{Ref: call.Reference, Revision: call.Routine.Revision(), Purpose: purpose}}
		if err := admitRoutineDescriptor(current, dependency[:], call.Routine, call.Consumer, call.followIdentity, purpose); err != nil {
			return call.body.Annotate(err)
		}
	}
	return nil
}

// A new trusted caller phase may borrow phase-independent cooked values only
// after admitting the exact original consumed graph under its own actual actor.
func (p *RoutineProgram) InitializePhase(original *RoutineProgram, auth RoutineAccess, phase RoutineReadmissionPurpose) error {
	if phase != CallerInitialization {
		return fmt.Errorf("stuntgres: invalid caller routine initialization purpose")
	}
	for _, call := range original.Calls {
		// A different phase has no active selection of this accepted DEFAULT.
		if call.defaultCall != nil {
			return ids.ErrCatalogReplaced
		}
		current := auth
		if call.body.Outer != nil {
			current.Sepgsql = access.SepgsqlAuthority{}
		}
		if call.SupportAggregate != nil {
			aggregate, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, call.SupportAggregate)
			if err != nil {
				return err
			}
			current.State.CurrentUser = aggregate.Owner
		}
		if handled, err := call.readmitNativeSource(current); handled {
			if err != nil {
				return call.body.Annotate(err)
			}
			call.Actor = current.State.CurrentUser
			p.Calls = append(p.Calls, call)
			continue
		}
		purpose := routineCapturePurpose(call.SupportAggregate)
		dependency := [1]catalog.ExpressionDependency{{Ref: call.Reference, Revision: call.Routine.Revision(), Purpose: purpose}}
		ref, err := routineDescriptorLifetime(current, dependency[:], call.Routine, call.Consumer, call.followIdentity, purpose)
		if err == nil {
			err = current.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege})
		}
		if err != nil {
			return call.body.Annotate(err)
		}
		call.Actor = current.State.CurrentUser
		p.Calls = append(p.Calls, call)
	}
	return nil
}

func (p *RoutineProgram) Include(other *RoutineProgram) {
	for _, call := range other.Calls {
		found := false
		for _, old := range p.Calls {
			if old.Reference == call.Reference && old.Routine == call.Routine && old.Recipe == call.Recipe && old.Consumer == call.Consumer && old.Actor == call.Actor && old.SupportAggregate == call.SupportAggregate && old.followIdentity == call.followIdentity && old.defaultCall == call.defaultCall && old.body == call.body && old.nativeSource == call.nativeSource {
				found = true
				break
			}
		}
		if !found {
			p.Calls = append(p.Calls, call)
		}
	}
}
