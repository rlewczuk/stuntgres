package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Only the constructor's actual fresh ref, immediately after the captured owner
// and bootstrap directives, can produce this pair. The Restore loop expires it
// on every subsequent statement; it is not general ALTER OWNER eligibility.
func (e *Engine) captureRestoredDomainOwner(op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (typedef.RestoredDomainOwner, error) {
	reject := func() (typedef.RestoredDomainOwner, error) {
		return typedef.RestoredDomainOwner{}, sqlError("0A000", typedef.RestoreDomainOwnerBoundary)
	}
	if op == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || created.Domain() != e.catalog.Domain || created.Epoch != auth.Catalog.Epoch() || created.Address.ClassID != ids.TypeClassID || created.Address.SubID != 0 {
		return reject()
	}
	if _, captured := e.importTargets[owner]; !captured {
		return reject()
	}
	domain, ok := auth.Catalog.Type(created.Address)
	if !ok || !domain.SQLType.IsDomain() {
		return reject()
	}
	namespace, ok := auth.Catalog.Namespace(domain.ObjectNamespace)
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	if !actor.Superuser || domain.Owner != actor.Ref || owner == actor.Ref {
		return reject()
	}
	if _, err := auth.Roles.Descriptor(owner); err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	return typedef.RestoredDomainOwner{Ref: created, Domain: domain, Namespace: namespace, Owner: owner, Creator: actor.Ref}, nil
}

func (e *Engine) restoreDomainOwner(op *session.Operation, raw *parser.AlterOwner, pair typedef.RestoredDomainOwner) error {
	reject := func() error { return sqlError("0A000", typedef.RestoreDomainOwnerBoundary) }
	if raw == nil || raw.Kind != "domain" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.Ref == (ids.ObjectRef{}) {
		return reject()
	}
	current, err := e.captureRestoredDomainOwner(op, pair.Ref, pair.Owner)
	if err != nil {
		return err
	}
	if current != pair || raw.Name.Parts[0].Text != pair.Namespace.Name() || raw.Name.Parts[1].Text != pair.Domain.Name() {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	owner, err := auth.Roles.Descriptor(pair.Owner)
	if err != nil {
		return err
	}
	if raw.Target.Name != owner.Name {
		return reject()
	}
	return nil
}
