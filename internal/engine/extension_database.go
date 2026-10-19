package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeDatabase(name string) {
	typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.databaseName, e.databaseNameError = name, err
	if err == nil {
		e.databaseName, _ = value.Text()
	}
	owner := e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	db := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: e.databaseName, Owner: owner}
	e.catalog.state.SetDescriptor(db.Address(), db)
	e.catalog.state.BindName(catalog.DescriptorNameKey(db), db.Address())
}

// Attachment identity and ACLs belong to the destination, not portable state.
// Retain them before replacement principal validation and publication.
func (e *Engine) retainDatabaseLocked(next *catalog.State) {
	if old := next.Get(catalog.CurrentDatabaseAddress); old != nil {
		next.UnbindName(catalog.DescriptorNameKey(old))
	}
	database := e.catalog.state.Get(catalog.CurrentDatabaseAddress)
	next.SetDescriptor(catalog.CurrentDatabaseAddress, database)
	next.BindName(catalog.DescriptorNameKey(database), catalog.CurrentDatabaseAddress)
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
	db := v.catalog.MustDatabase(catalog.CurrentDatabaseAddress)
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	source, available, err := authorization.ObjectGrantSource(op.Context(), db.Owner, db.ACL, authorization.ACLValue{}, principals, actor.Superuser, requested)
	if err != nil {
		return Result{}, err
	}
	if available == 0 && authorization.ACLRights(baseline, db.Owner, principals, authorization.DatabasePrivileges, false) == 0 {
		return Result{}, sqlError("42501", "permission denied for database "+dependencyIdentifier(db.Name()))
	}
	grantWarning(op, db.Name(), "", requested, available, all, s.Revoke)
	if available == 0 {
		return Result{}, nil
	}
	acl, changed, err := authorization.ChangeObjectACL(op.Context(), v.roles, baseline, db.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil || !changed && db.ACL.Explicit {
		return Result{}, err
	}
	next := *db
	next.ACL = acl
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	if err := validateACL(v.roles, &next); err != nil {
		return Result{}, err
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.catalogChangeLocked(changes, db.Address(), &next)
	scope.catalog = changes
	scope.catalogAuthority = v.catalogAuthorityLocked(ids.ObjectRef{Address: db.Address()}, e.catalog.state.Get(db.Address()))
	return Result{}, nil
}
