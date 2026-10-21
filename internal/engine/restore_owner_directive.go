package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Restore hook entries for owner directives (SET SESSION AUTHORIZATION).
// A superuser directive after another owner's directive makes that owner the
// owner of the next CREATE; the created domain or inheriting table is then
// paired with the ALTER OWNER that follows it.

// beforeRestoreOwnerSnapshot moves the owner directive state into the
// current statement and clears it, so it reaches only the next statement.
func beforeRestoreOwnerSnapshot(r *restoreRun, _ *session.Operation, _ parser.Statement) (bool, error) {
	r.cur.priorAuthorization, r.cur.ownerForCreate, r.cur.ownerPair = r.lastAuthorization, r.createOwner, r.pairedOwner
	r.cur.domainOwnerPair = r.pairedDomainOwner
	r.pairedDomainOwner = typedef.RestoredDomainOwner{}
	r.lastAuthorization, r.createOwner = authorization.PrincipalRef{}, authorization.PrincipalRef{}
	r.pairedOwner = restoredInheritedOwner{}
	return false, nil
}

// afterRestoreOwnerDirective records the principal an owner directive
// switched to, and the create owner it implies.
func afterRestoreOwnerDirective(r *restoreRun, op *session.Operation, stmt parser.Statement, _ Result) error {
	directive, ok := stmt.(*parser.SessionAuthorization)
	if !ok || !directive.OwnerDirective || directive.Default {
		return nil
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser && r.cur.priorAuthorization != (authorization.PrincipalRef{}) && r.cur.priorAuthorization != actor.Ref {
		r.createOwner = r.cur.priorAuthorization
	}
	r.lastAuthorization = actor.Ref
	return nil
}
