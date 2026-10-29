package role

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
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
		case *catalog.Publication:
			err = access.PublicationOwnershipEligibility(view, auth, d.(*catalog.Publication), target)
		case *catalog.EventTrigger:
			err = access.EventTriggerOwnershipEligibility(view, auth, d.(*catalog.EventTrigger), target)
		case *catalog.ForeignObject:
			foreign := d.(*catalog.ForeignObject)
			err = access.ForeignOwnershipEligibility(view, auth, foreign, target)
			if err == nil && foreign.Address().ClassID == ids.ForeignServerClassID {
				h.AddDomainBase(view.Catalog.Get(foreign.Wrapper))
			}
		case *catalog.Namespace, *catalog.Subscription:
			err = view.Check(auth, access.Requirement{Object: stage.BaseRef(a), OwnerOnly: true})
			if sub, ok := d.(*catalog.Subscription); err == nil && ok && !sub.PasswordRequired {
				actor, failure := view.Roles.Descriptor(view.State.CurrentUser)
				err = failure
				if err == nil && !actor.Superuser {
					err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "password_required=false is superuser-only", Hint: "Subscriptions with the password_required option set to false may only be created or modified by the superuser."}
				}
			}
			if err == nil {
				err = access.OwnershipSuccessor(view, target)
			}
			if err == nil {
				err = access.OwnershipDatabaseCreate(view, auth)
			}
		case *catalog.Statistics, *catalog.Collation, *catalog.Conversion, *catalog.Operator, *catalog.Database, *catalog.Extension, *catalog.Procedure, *catalog.Language, *catalog.TSDictionary, *catalog.TSConfiguration:
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
	for address, record := range h.StageRead().State().InitialPrivileges() {
		acl := record.ACL
		changed := false
		for ref := range access.InitialACLRoleReferences(record) {
			if !plan.Sources[ref] || ref == target {
				continue
			}
			// The immutable input may mention this source more than once;
			// do not copy again after its first complete replacement.
			for _, entry := range acl.Entries {
				if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
					acl = authorization.SchemaACLOwner(acl, ref, target)
					changed = true
					break
				}
			}
		}
		if changed {
			stage.RecordInitialPrivilege(address, record.PrivType, acl)
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
