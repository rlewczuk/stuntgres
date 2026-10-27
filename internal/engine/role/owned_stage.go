package role

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

// StageOwned stages a planned REASSIGN OWNED (each root's eligibility, its
// new owner and the expressions of changed providers) or DROP OWNED (each
// root's removal). It runs with Engine.mu held, after the lock waits.
func StageOwned(h StageHost, auth ddl.Authority, plan OwnedPlan) error {
	view := h.AccessView(auth)
	stage := h.OwnerStage()
	base := h.StageBase()
	if !plan.Reassign {
		for _, a := range plan.Roots {
			if err := h.RemoveObject(stage.BaseRef(a), a.ClassID == ids.NamespaceClassID); err != nil {
				return err
			}
		}
		return nil
	}
	target := plan.Target
	providersChanged := false
	for _, a := range plan.Roots {
		d := base.Get(a)
		owner, _, _ := access.DescriptorACL(d)
		if owner == target {
			continue
		}
		var err error
		// PostgreSQL's schema and generic collation handlers retain their
		// own eligibility checks; relation/type bulk recursion does not SET.
		switch d.(type) {
		case *catalog.EventTrigger:
			err = view.Check(auth, access.Requirement{Object: stage.BaseRef(a), OwnerOnly: true})
			if err == nil {
				successor, failure := view.Roles.Descriptor(target)
				err = failure
				if err == nil && !successor.Superuser {
					err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to change owner of event trigger "%s"`, d.Name()), Hint: "The owner of an event trigger must be a superuser."}
				}
			}
		case *catalog.ForeignObject:
			foreign := d.(*catalog.ForeignObject)
			err = access.ForeignOwnershipEligibility(view, auth, foreign, target)
			if err == nil && foreign.Address().ClassID == ids.ForeignServerClassID {
				h.AddDomainBase(view.Catalog.Get(foreign.Wrapper))
			}
		case *catalog.Namespace:
			err = view.Check(auth, access.Requirement{Object: stage.BaseRef(a), OwnerOnly: true})
			if err == nil {
				err = access.OwnershipSuccessor(view, target)
			}
			if err == nil {
				err = access.OwnershipDatabaseCreate(view, auth)
			}
		case *catalog.Statistics, *catalog.Collation, *catalog.Conversion, *catalog.Operator, *catalog.Database, *catalog.Extension, *catalog.Procedure, *catalog.TSDictionary, *catalog.TSConfiguration:
			err = view.Check(auth, access.Requirement{Object: stage.BaseRef(a), OwnerOnly: true})
			if err == nil {
				err = access.OwnershipEligibility(view, auth, target, d.Namespace())
			}
		}
		if err != nil {
			return err
		}
		if err = StageOwnership(stage, d, target); err != nil {
			return err
		}
		switch d.(type) {
		case *catalog.Procedure, *catalog.Operator, *catalog.TSDictionary, *catalog.TSConfiguration:
			providersChanged = true
		}
	}
	if !providersChanged {
		return nil
	}
	read := h.StageRead()
	candidate := read.State()
	if err := stored.ReownExpressions(read, candidate, nil, false, nil); err != nil {
		return err
	}
	for a, d := range candidate.Descriptors() {
		if old := stage.Get(a); old != nil {
			if old == d {
				continue
			}
		} else if base.Get(a) == d {
			continue
		}
		if err := stage.Replace(d); err != nil {
			return err
		}
	}
	return nil
}

// OwnedDependentsError rewords DROP OWNED's 2BP01 dependents error: it names
// primary, the one object the statement drops, or the dropped objects in
// general when primary is empty.
func OwnedDependentsError(err error, primary string) error {
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" {
		if primary != "" {
			diagnostic.Message = "cannot drop " + primary + " because other objects depend on it"
		} else {
			diagnostic.Message = "cannot drop desired object(s) because other objects depend on them"
		}
	}
	return err
}
