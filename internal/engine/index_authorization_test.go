package engine

import (
	"context"
	"testing"
)

func TestBtreeIndexOwnerAndNamespaceAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	roleExecute(t, e, op, "CREATE TABLE t(n INT)")
	roleExecute(t, e, op, "CREATE INDEX idx ON t(n)")
	ref := roleTableRef(t, e, "t")
	owner := e.roles.names["owner"]
	reader := e.roles.names["reader"]
	if err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: reader}, Privileges: SelectPrivilege | InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleError(t, e, op, "CREATE INDEX forbidden ON t(n)", "42501", "must be owner of table t", "")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	ns := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}
	if err := e.SetObjectACL(context.Background(), ns, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	roleError(t, e, op, "CREATE INDEX forbidden ON t(n)", "42501", "permission denied for schema public", "")
	if len(e.indexRoots) != 1 {
		t.Fatal("denied index leaked roots")
	}
}
func TestBtreeIndexPrivateTypeSavepointAndDrop(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT,s TEXT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,'a'),(2,'b'),(10,'c')")
	sessionSQL(t, s, "CREATE INDEX ni ON t(n)")
	sessionSQL(t, s, "CREATE INDEX si ON t(s)")
	old := btreeWantKeys(t, e, "ni", "1", "2", "10")
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "SAVEPOINT before_type")
	sessionSQL(t, s, "ALTER TABLE t ALTER n TYPE TEXT USING n::text")
	if _, root := btreeRoot(t, e, "ni"); root != old {
		t.Fatal("private type published root")
	}
	sessionSQL(t, s, "ROLLBACK TO before_type")
	transactionEnd(t, s, tx, true)
	btreeWantKeys(t, e, "ni", "1", "2", "10")
	tx = transactionBegin(t, s)
	sessionSQL(t, s, "ALTER TABLE t ALTER n TYPE TEXT USING n::text")
	transactionEnd(t, s, tx, true)
	btreeWantKeys(t, e, "ni", "1", "10", "2")
	tx = transactionBegin(t, s)
	sessionSQL(t, s, "ALTER TABLE t DROP s")
	transactionEnd(t, s, tx, false)
	btreeWantKeys(t, e, "si", "a", "b", "c")
	tx = transactionBegin(t, s)
	sessionSQL(t, s, "ALTER TABLE t DROP s")
	transactionEnd(t, s, tx, true)
	if len(e.indexRoots) != 1 {
		t.Fatal("private drop left orphan root")
	}
}
