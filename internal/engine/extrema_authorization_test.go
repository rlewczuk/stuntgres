package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

// Exercise the actual package ACL seam. SQL GRANT is not a native public API;
// shared reference permissions also cover ordinary and held statement rights.
func TestExtremaArgumentColumnAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER extrema_acl_reader")
	roleExecute(t, e, op, "CREATE TABLE extrema_secret(v INT,k INT)")
	ref := roleTableRef(t, e, "extrema_secret")
	reader, _ := e.Roles().Lookup("extrema_acl_reader")
	column := ref
	column.Address.SubID = 1
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := e.SetObjectACL(context.Background(), column, grant); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION extrema_acl_reader")
	for _, q := range []string{"SELECT min(v),max(v) FROM extrema_secret", "SELECT min(v),max(v) FROM extrema_secret WHERE FALSE"} {
		roleExecute(t, e, op, q).Rows.Close()
	}
	for _, q := range []string{
		"SELECT min(k) FROM extrema_secret",
		"SELECT max(k) FROM extrema_secret WHERE FALSE",
		"SELECT min(v+k) FROM extrema_secret",
		"SELECT min(v) FROM extrema_secret HAVING max(k)>0",
		"SELECT min(v) FROM extrema_secret ORDER BY max(k)",
	} {
		roleError(t, e, op, q, "42501", "permission denied for table extrema_secret", "")
	}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT min(v),max(v) FROM extrema_secret", "SELECT min(v),max(v) FROM extrema_secret WHERE FALSE"} {
		roleError(t, e, op, q, "42501", "permission denied for table extrema_secret", "")
	}
}
