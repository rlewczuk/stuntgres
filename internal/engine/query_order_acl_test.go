package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestQueryOrderACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER order_reader")
	roleExecute(t, e, op, "CREATE TABLE acl_t(id INT,secret INT)")
	roleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,9),(2,8)")
	ref := roleTableRef(t, e, "acl_t")
	column := ref
	column.Address.SubID = 1
	reader, _ := e.Roles().Lookup("order_reader")
	entry := authorization.ACLEntry{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION order_reader")
	for _, q := range []string{"SELECT id AS secret FROM acl_t ORDER BY secret", "SELECT id AS secret FROM acl_t ORDER BY 1", "SELECT id FROM acl_t ORDER BY id+0", "SELECT a.id FROM acl_t a JOIN acl_t b USING(id) ORDER BY a.id+b.id"} {
		r := roleExecute(t, e, op, q)
		if r.Count != 2 {
			t.Fatal(q, r.Count)
		}
		r.Rows.Close()
	}
	for _, q := range []string{"SELECT id FROM acl_t ORDER BY secret+1", "SELECT id AS secret FROM acl_t ORDER BY acl_t.secret", "SELECT a.id FROM acl_t a JOIN acl_t b USING(id) ORDER BY b.secret+1"} {
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal("eager ACL denial", q, err)
		}
		roleError(t, e, op, q, "42501", "permission denied for table acl_t", "")
	}
}
