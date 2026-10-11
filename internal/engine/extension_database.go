package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var currentDatabaseAddress = ObjectAddress{ClassID: DatabaseClassID, ObjectID: 16383}

type databaseDescriptor struct {
	address  ObjectAddress
	revision DefinitionRevision
	name     string
	owner    PrincipalRef
	acl      ACLValue
}

func (d *databaseDescriptor) Address() ObjectAddress       { return d.address }
func (d *databaseDescriptor) Revision() DefinitionRevision { return d.revision }
func (*databaseDescriptor) Namespace() ObjectAddress       { return ObjectAddress{} }
func (d *databaseDescriptor) Name() string                 { return d.name }
func (*databaseDescriptor) Kind() ObjectKind               { return DatabaseObject }

type databaseProvider struct{}

func (databaseProvider) ClassID() OID { return DatabaseClassID }
func (databaseProvider) Validate(v CatalogRead, d Descriptor) error {
	db, ok := d.(*databaseDescriptor)
	if !ok || db.address != currentDatabaseAddress || db.revision == 0 || v.state.names[descriptorNameKey(d)] != db.address || db.Namespace() != (ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid current database descriptor")
	}
	return nil
}
func (e *Engine) initializeDatabase(name string) {
	typ, _ := (CatalogRead{e, e.state}).LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.databaseName, e.databaseNameError = name, err
	if err == nil {
		e.databaseName, _ = value.Text()
	}
	owner := e.roles.names["postgres"]
	db := &databaseDescriptor{address: currentDatabaseAddress, revision: 1, name: e.databaseName, owner: owner, acl: ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: owner}, Privileges: CreatePrivilege, GrantOptions: CreatePrivilege}}}}
	e.state.descriptors[db.address] = db
	e.state.names[descriptorNameKey(db)] = db.address
}

// Attachment identity and ACLs belong to the destination, not portable state.
// Retain them before replacement principal validation and publication.
func (e *Engine) retainDatabaseLocked(next *catalogState) {
	if old := next.descriptors[currentDatabaseAddress]; old != nil {
		delete(next.names, descriptorNameKey(old))
	}
	database := e.state.descriptors[currentDatabaseAddress]
	next.descriptors[currentDatabaseAddress] = database
	next.names[descriptorNameKey(database)] = currentDatabaseAddress
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
	if s.Kind != "database" || len(s.Relations) != 1 || len(s.Schemas) != 0 {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	name := s.Relations[0]
	if len(name.Parts) != 1 {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	if name.Parts[0].Text != e.databaseName {
		return Result{}, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, name.Parts[0].Text))
	}
	for _, item := range s.Privileges {
		if item.All || len(item.Columns) != 0 || !strings.EqualFold(item.Name, "create") {
			return Result{}, sqlError("0A000", "only CREATE privilege on the current database is supported")
		}
	}
	if s.Grantor != nil {
		grantor, err := membershipRole(v.roles, v.state, *s.Grantor)
		if err != nil {
			return Result{}, err
		}
		if grantor != v.state.CurrentUser {
			return Result{}, sqlError("0A000", "grantor must be current user")
		}
	}
	grantees := make([]ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			if s.GrantOption {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			role, err := membershipRole(v.roles, v.state, spec.Role)
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
	principals, err := v.roles.roleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	db := v.catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	source, available, err := objectGrantSource(op.Context(), db.owner, db.acl, ACLValue{}, principals, actor.Superuser, CreatePrivilege)
	if err != nil {
		return Result{}, err
	}
	if available == 0 && aclRights(db.acl, db.owner, principals, CreatePrivilege, false) == 0 {
		return Result{}, sqlError("42501", "permission denied for database "+db.name)
	}
	grantWarning(op, db.name, "", CreatePrivilege, available, false, s.Revoke)
	if available == 0 {
		return Result{}, nil
	}
	acl, changed, err := changeObjectACL(op.Context(), v.roles, db.acl, db.owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil || !changed {
		return Result{}, err
	}
	next := *db
	next.acl = acl
	next.revision, err = e.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	if err := validateACL(v.roles, &next); err != nil {
		return Result{}, err
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.catalogChangeLocked(changes, db.address, &next)
	scope.catalog = changes
	scope.catalogAuthority = &catalogAuthority{session: op.session, authorization: v.revision, roles: v.roles.state.revision, ref: ObjectRef{Address: db.address}, descriptor: e.state.descriptors[db.address]}
	return Result{}, nil
}
