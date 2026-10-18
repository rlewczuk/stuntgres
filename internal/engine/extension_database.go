package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var currentDatabaseAddress = ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 16383}

type databaseDescriptor struct {
	address  ids.ObjectAddress
	revision ids.DefinitionRevision
	name     string
	Owner    authorization.PrincipalRef
	ACL      authorization.ACLValue
}

func (d *databaseDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *databaseDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (*databaseDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *databaseDescriptor) Name() string                     { return d.name }
func (*databaseDescriptor) Kind() ids.ObjectKind               { return ids.DatabaseObject }

type databaseProvider struct{}

func (databaseProvider) ClassID() ids.OID { return ids.DatabaseClassID }
func (databaseProvider) Validate(v CatalogRead, d Descriptor) error {
	db, ok := d.(*databaseDescriptor)
	if !ok || db.address != currentDatabaseAddress || db.revision == 0 || v.NameAddress(descriptorNameKey(d)) != db.address || db.Namespace() != (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid current database descriptor")
	}
	return nil
}
func (e *Engine) initializeDatabase(name string) {
	typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.databaseName, e.databaseNameError = name, err
	if err == nil {
		e.databaseName, _ = value.Text()
	}
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	db := &databaseDescriptor{address: currentDatabaseAddress, revision: 1, name: e.databaseName, Owner: owner}
	e.catalog.state.setDescriptor(db.address, db)
	e.catalog.state.bindName(descriptorNameKey(db), db.address)
}

// Attachment identity and ACLs belong to the destination, not portable state.
// Retain them before replacement principal validation and publication.
func (e *Engine) retainDatabaseLocked(next *catalogState) {
	if old := next.Get(currentDatabaseAddress); old != nil {
		next.unbindName(descriptorNameKey(old))
	}
	database := e.catalog.state.Get(currentDatabaseAddress)
	next.setDescriptor(currentDatabaseAddress, database)
	next.bindName(descriptorNameKey(database), currentDatabaseAddress)
}
func (e *Engine) executeDatabaseGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if s.Kind != "database" || len(s.Relations) == 0 || len(s.Schemas) != 0 {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	var requested authorization.Privileges
	all := false
	for _, item := range s.Privileges {
		mask, err := authorization.ObjectPrivilege(item, "database")
		if err != nil {
			return Result{}, err
		}
		requested |= mask
		all = all || item.All
	}
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return Result{}, sqlError("0A000", "statement is not supported")
		}
		if name.Parts[0].Text != e.databaseName {
			return Result{}, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, name.Parts[0].Text))
		}
	}
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(v.roles, v.state, *s.Grantor)
		if err != nil {
			return Result{}, err
		}
		if grantor != v.state.CurrentUser {
			return Result{}, sqlError("0A000", "grantor must be current user")
		}
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			if s.GrantOption {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			role, err := authorization.MembershipRole(v.roles, v.state, spec.Role)
			if err != nil {
				return Result{}, err
			}
			grantees[i].Role = role
		}
	}
	actor, err := v.roles.Descriptor(v.state.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	principals, err := v.roles.RoleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	db := v.catalog.MustDatabase(currentDatabaseAddress)
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	source, available, err := authorization.ObjectGrantSource(op.Context(), db.Owner, db.ACL, authorization.ACLValue{}, principals, actor.Superuser, requested)
	if err != nil {
		return Result{}, err
	}
	if available == 0 && authorization.ACLRights(baseline, db.Owner, principals, authorization.DatabasePrivileges, false) == 0 {
		return Result{}, sqlError("42501", "permission denied for database "+dependencyIdentifier(db.name))
	}
	grantWarning(op, db.name, "", requested, available, all, s.Revoke)
	if available == 0 {
		return Result{}, nil
	}
	acl, changed, err := authorization.ChangeObjectACL(op.Context(), v.roles, baseline, db.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil || !changed && db.ACL.Explicit {
		return Result{}, err
	}
	next := *db
	next.ACL = acl
	next.revision, err = e.catalog.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	if err := validateACL(v.roles, &next); err != nil {
		return Result{}, err
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.catalogChangeLocked(changes, db.address, &next)
	scope.catalog = changes
	scope.catalogAuthority = v.catalogAuthorityLocked(ids.ObjectRef{Address: db.address}, e.catalog.state.Get(db.address))
	return Result{}, nil
}
