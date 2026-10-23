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
func (e *Engine) captureRestoredTypeOwner(op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef, kind string) (typedef.RestoredTypeOwner, error) {
	reject := func() (typedef.RestoredTypeOwner, error) {
		return typedef.RestoredTypeOwner{}, sqlError("0A000", typedef.RestoreTypeOwnerBoundary(kind))
	}
	if op == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return typedef.RestoredTypeOwner{}, err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return typedef.RestoredTypeOwner{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || created.Domain() != e.catalog.Domain || created.Epoch != auth.Catalog.Epoch() || created.Address.ClassID != ids.TypeClassID || created.Address.SubID != 0 {
		return reject()
	}
	if _, captured := e.importTargets[owner]; !captured {
		return reject()
	}
	typ, ok := auth.Catalog.Type(created.Address)
	if !ok || kind != "domain" && kind != "type" {
		return reject()
	}
	if kind == "domain" && !typ.SQLType.IsDomain() {
		return reject()
	}
	if kind == "type" {
		if !typ.SQLType.IsRange() {
			return reject()
		}
		if _, genuine := typ.SQLType.RangeDefinition(); !genuine {
			return reject()
		}
	}
	namespace, ok := auth.Catalog.Namespace(typ.ObjectNamespace)
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return typedef.RestoredTypeOwner{}, err
	}
	if !actor.Superuser || typ.Owner != actor.Ref || owner == actor.Ref {
		return reject()
	}
	if _, err := auth.Roles.Descriptor(owner); err != nil {
		return typedef.RestoredTypeOwner{}, err
	}
	return typedef.RestoredTypeOwner{Ref: created, Type: typ, Kind: kind, Namespace: namespace, Owner: owner, Creator: actor.Ref}, nil
}

func (e *Engine) restoreTypeOwner(op *session.Operation, raw *parser.AlterOwner, pair typedef.RestoredTypeOwner) error {
	kind := pair.Kind
	if raw != nil {
		kind = raw.Kind
	}
	reject := func() error { return sqlError("0A000", typedef.RestoreTypeOwnerBoundary(kind)) }
	if raw == nil || raw.Kind != pair.Kind || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.Ref == (ids.ObjectRef{}) {
		return reject()
	}
	current, err := e.captureRestoredTypeOwner(op, pair.Ref, pair.Owner, pair.Kind)
	if err != nil {
		return err
	}
	if current != pair || raw.Name.Parts[0].Text != pair.Namespace.Name() || raw.Name.Parts[1].Text != pair.Type.Name() {
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
