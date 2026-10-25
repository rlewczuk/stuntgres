package indexcmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestTransactionalIndexAuthorityFences(t *testing.T) {
	for _, nameWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent-wait", true: "conditional-name-wait"}[nameWait], func(t *testing.T) {
			e := engine.New()
			root, actor := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE USER actor", "CREATE TABLE t(n INT)", "ALTER TABLE t OWNER TO actor", "CREATE TABLE other(n INT)"} {
				enginetest.SessionSQL(t, root, q)
			}
			ns := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
			roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
			grantor, grantee := roles.LookupRef("postgres"), roles.LookupRef("actor")
			set := func(rights authorization.Privileges) {
				t.Helper()
				if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			set(authorization.UsagePrivilege | authorization.CreatePrivilege)
			enginetest.SessionSQL(t, actor, "SET ROLE actor")
			// Describe must neither authorize nor enqueue nor construct a candidate.
			set(authorization.UsagePrivilege)
			op := enginetest.TransactionOperation(t, actor)
			_, err := e.Describe(op, enginetest.Statement(t, "CREATE INDEX idx ON t(n)"))
			op.Done()
			if err != nil {
				t.Fatal("Describe authorized target", err)
			}
			if len(e.Locks.Inspect().Resources) != 0 || actor.Transactions.Catalog != nil {
				t.Fatal("Describe acquired resources or staged roots")
			}
			set(authorization.UsagePrivilege | authorization.CreatePrivilege)
			rt := enginetest.TransactionBegin(t, root)
			q := "CREATE INDEX idx ON t(n)"
			if nameWait {
				enginetest.SessionSQL(t, root, "CREATE INDEX idx ON other(n)")
				q = "CREATE INDEX IF NOT EXISTS idx ON t(n)"
			} else {
				enginetest.SessionSQL(t, root, "INSERT INTO t VALUES(1)")
			}
			at := enginetest.TransactionBegin(t, actor)
			done := transactionalIndexAsync(t, e, actor, q)
			enginetest.LockPending(t, e, 1)
			set(authorization.UsagePrivilege)
			// Rollback releases the competing claim without publishing a journal
			// whose namespace predecessor this ACL fixture deliberately changed.
			// The committed-winner collision precedence is covered in reference.
			enginetest.TransactionEnd(t, root, rt, false)
			enginetest.State(t, <-done, "42501")
			enginetest.TransactionEnd(t, actor, at, false)
			if !nameWait && len(e.Storage.IndexRoots) != 0 {
				t.Fatal("authority failure leaked root")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	// Same exact identity with a compatible revision remains admissible.
	t.Run("compatible-revision", func(t *testing.T) {
		e := engine.New()
		a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
		enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
		at := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
		bt := enginetest.TransactionBegin(t, b)
		done := transactionalIndexAsync(t, e, b, "CREATE INDEX idx ON t(n)")
		enginetest.LockPending(t, e, 1)
		base := e.CatalogRead()
		ref := enginetest.RoleTableRef(t, e, "t")
		old := base.MustRelation(ref.Address)
		next := *old
		next.ObjectRevision++
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
		enginetest.TransactionEnd(t, a, at, false)
		enginetest.LockAwait(t, done, nil)
		enginetest.TransactionEnd(t, b, bt, true)
		btreeWantKeys(t, e, "idx", "1")
		enginetest.LockEmpty(t, e)
	})
}
