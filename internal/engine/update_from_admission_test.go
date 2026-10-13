package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
	"time"
)

func TestUpdateFromEarlierSourceMetadataAndAuthorityRefresh(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "comment", true: "acl"}[revoked], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE USER admission_writer", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1,100)", "INSERT INTO r VALUES(1)"} {
				sessionSQL(t, peer, q)
			}
			actor, _ := e.Roles().Lookup("admission_writer")
			owner, _ := e.Roles().Lookup("postgres")
			grant := func(table string, mask authorization.Privileges) {
				t.Helper()
				ref := roleTableRef(t, e, table)
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant("t", authorization.UpdatePrivilege|authorization.SelectPrivilege)
			grant("s", authorization.SelectPrivilege)
			grant("r", authorization.SelectPrivilege)
			sessionSQL(t, writer, "SET SESSION AUTHORIZATION admission_writer")
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK TABLE r IN ACCESS EXCLUSIVE MODE")
			done := lockAsync(e, writer, context.Background(), "UPDATE t SET v=s.n FROM s JOIN r USING(k) WHERE t.id=k RETURNING old.v,new.v,s.n", false)
			lockPending(t, e, 1)
			sref := catalogRef(t, e, e.CatalogRead(), "s")
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: sref}]
			firstAdmitted := entry != nil && len(entry.holders) == 1
			e.locks.mu.Unlock()
			if !firstAdmitted {
				t.Fatal("earlier source was not genuinely admitted")
			}
			sessionSQL(t, peer, "COMMENT ON TABLE s IS 'UPDATE FROM earlier source revision'")
			if revoked {
				grant("s", 0)
			}
			transactionEnd(t, holder, tx, false)
			if revoked {
				select {
				case err := <-done:
					waitsDiagnostic(t, err, "42501", "permission denied for table s")
				case <-time.After(5 * time.Second):
					t.Fatal("revoked source waiter did not finish")
				}
				transactionRows(t, peer, "SELECT v FROM t", 10)
			} else {
				lockAwait(t, done, nil)
				transactionRows(t, peer, "SELECT v FROM t", 100)
			}
			if e.CatalogRead().state.descriptors[sref.Address].(*relationDescriptor).comment != "UPDATE FROM earlier source revision" {
				t.Fatal("compatible source comment revision lost")
			}
			lockEmpty(t, e)
		})
	}
}
