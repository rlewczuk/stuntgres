package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
)

// Native has no public ACL mutation API. Keep this genuine Engine.SetObjectACL
// authority regression internal; TestReferenceLikeOracleSecurity independently
// covers the PostgreSQL public column-GRANT routes, without fake native grants.
func TestLikePatternEscapeColumnACL(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER like_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE acl_t(id INT,p TEXT,e TEXT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,'a%','!')")
	ref := enginetest.RoleTableRef(t, e, "acl_t")
	reader, _ := e.Roles().Lookup("like_reader")
	entry := authorization.ACLEntry{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	grant := func(sub int32) {
		column := ref
		column.Address.SubID = sub
		if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
			t.Fatal(err)
		}
	}
	grant(1)
	for _, empty := range []bool{false, true} {
		if empty {
			enginetest.RoleExecute(t, e, op, "DELETE FROM acl_t")
		}
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION like_reader")
		for _, q := range []string{"SELECT id FROM acl_t WHERE 'a' LIKE p", "SELECT id FROM acl_t WHERE 'a' LIKE 'a' ESCAPE e", "SELECT id FROM acl_t WHERE FALSE AND 'a' LIKE p ESCAPE e"} {
			if _, err := e.Describe(op, enginetest.Statement(t, q)); err != nil {
				t.Fatal("eager ACL denial", q, err)
			}
			enginetest.RoleError(t, e, op, q, "42501", "permission denied for table acl_t", "")
		}
		enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	grant(2)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION like_reader")
	enginetest.RoleExecute(t, e, op, "SELECT id FROM acl_t WHERE 'a' LIKE p").Rows.Close()
	enginetest.RoleError(t, e, op, "SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e", "42501", "permission denied for table acl_t", "")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(3)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION like_reader")
	enginetest.RoleExecute(t, e, op, "SELECT id FROM acl_t WHERE 'a' LIKE p ESCAPE e").Rows.Close()
}
