package functions

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ACLAuthoritySource supplies the ACL identity of the executing statement's
// role store. aclitem values carry that identity; a nil result means the
// evaluation has no live statement scope.
type ACLAuthoritySource interface {
	ACLAuthority() *sqltype.ACLAuthority
}

// ACLRuntime is the evaluation capability of the ACL SQL functions. The
// engine's evaluation context implements it; nodes recover it from
// expr.Context by assertion, so expr.Context does not name ACL state. Each
// inquiry borrows the executing statement's authority, catalog and role
// snapshot for one call and retains nothing.
type ACLRuntime interface {
	ACLAuthoritySource
	// ObjectACL is pg_get_acl: the raw ACL of the object at address, as a
	// result-typed aclitem[] value, or SQL NULL when it has none.
	ObjectACL(address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error)
	// RoleMembership is pg_has_role. A nil actor is the expression's current
	// user. privilege is the unparsed privilege list.
	RoleMembership(actor *AccessRole, target AccessRole, privilege string) (bool, error)
	// RowSecurityActive is row_security_active for one relation.
	RowSecurityActive(relation AccessRelation) (bool, error)
	// ObjectPrivilege returns exists=false for an absent object OID.
	ObjectPrivilege(kind AccessKind, actor *AccessRole, object AccessRole, privilege string) (value, exists bool, err error)
}

// AccessRole is a role argument of pg_has_role: an OID, or a name the
// runtime looks up in the statement's role snapshot.
type AccessRole struct {
	ByOID bool
	OID   uint32
	Name  string
}

// AccessRelation is the relation argument of row_security_active: an OID, or
// the regclass text the runtime resolves on the statement's search path.
type AccessRelation struct {
	ByOID bool
	OID   uint32
	Name  string
}
