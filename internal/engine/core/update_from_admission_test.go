package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestUpdateFromEarlierSourceMetadataAndAuthorityRefresh(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "comment", true: "acl"}[revoked], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE USER admission_writer", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1,100)", "INSERT INTO r VALUES(1)"} {
				enginetest.SessionSQL(t, peer, q)
			}
			actor, _ := e.Roles().Lookup("admission_writer")
			owner, _ := e.Roles().Lookup("postgres")
			grant := func(table string, mask authorization.Privileges) {
				t.Helper()
				ref := enginetest.RoleTableRef(t, e, table)
				if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant("t", authorization.UpdatePrivilege|authorization.SelectPrivilege)
			grant("s", authorization.SelectPrivilege)
			grant("r", authorization.SelectPrivilege)
			enginetest.SessionSQL(t, writer, "SET SESSION AUTHORIZATION admission_writer")
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "LOCK TABLE r IN ACCESS EXCLUSIVE MODE")
			done := enginetest.LockAsync(e, writer, context.Background(), "UPDATE t SET v=s.n FROM s JOIN r USING(k) WHERE t.id=k RETURNING old.v,new.v,s.n", false)
			enginetest.LockPending(t, e, 1)
			sref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: sref}]
			firstAdmitted := entry != nil && len(entry.Holders) == 1
			if !firstAdmitted {
				t.Fatal("earlier source was not genuinely admitted")
			}
			enginetest.SessionSQL(t, peer, "COMMENT ON TABLE s IS 'UPDATE FROM earlier source revision'")
			if revoked {
				grant("s", 0)
			}
			enginetest.TransactionEnd(t, holder, tx, false)
			if revoked {
				select {
				case err := <-done:
					waitsDiagnostic(t, err, "42501", "permission denied for table s")
				case <-time.After(5 * time.Second):
					t.Fatal("revoked source waiter did not finish")
				}
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
			} else {
				enginetest.LockAwait(t, done, nil)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 100)
			}
			if e.CatalogRead().MustRelation(sref.Address).Comment != "UPDATE FROM earlier source revision" {
				t.Fatal("compatible source comment revision lost")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
