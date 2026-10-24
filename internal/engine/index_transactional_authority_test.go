package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestTransactionalIndexAuthorityFences(t *testing.T) {
	for _, nameWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent-wait", true: "conditional-name-wait"}[nameWait], func(t *testing.T) {
			e := New()
			root, actor := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE USER actor", "CREATE TABLE t(n INT)", "ALTER TABLE t OWNER TO actor", "CREATE TABLE other(n INT)"} {
				sessionSQL(t, root, q)
			}
			ns := e.catalog.Domain.Ref(e.catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
			roles := e.roles.published.Root.Read(authorization.PrincipalRef{})
			grantor, grantee := roles.LookupRef("postgres"), roles.LookupRef("actor")
			set := func(rights authorization.Privileges) {
				t.Helper()
				if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			set(authorization.UsagePrivilege | authorization.CreatePrivilege)
			sessionSQL(t, actor, "SET ROLE actor")
			// Describe must neither authorize nor enqueue nor construct a candidate.
			set(authorization.UsagePrivilege)
			op := transactionOperation(t, actor)
			_, err := e.Describe(op, statement(t, "CREATE INDEX idx ON t(n)"))
			op.Done()
			if err != nil {
				t.Fatal("Describe authorized target", err)
			}
			if len(e.locks.Inspect().Resources) != 0 || actor.Transactions.Catalog != nil {
				t.Fatal("Describe acquired resources or staged roots")
			}
			set(authorization.UsagePrivilege | authorization.CreatePrivilege)
			rt := transactionBegin(t, root)
			q := "CREATE INDEX idx ON t(n)"
			if nameWait {
				sessionSQL(t, root, "CREATE INDEX idx ON other(n)")
				q = "CREATE INDEX IF NOT EXISTS idx ON t(n)"
			} else {
				sessionSQL(t, root, "INSERT INTO t VALUES(1)")
			}
			at := transactionBegin(t, actor)
			done := transactionalIndexAsync(t, e, actor, q)
			lockPending(t, e, 1)
			set(authorization.UsagePrivilege)
			// Rollback releases the competing claim without publishing a journal
			// whose namespace predecessor this ACL fixture deliberately changed.
			// The committed-winner collision precedence is covered in reference.
			transactionEnd(t, root, rt, false)
			state(t, <-done, "42501")
			transactionEnd(t, actor, at, false)
			if !nameWait && len(e.storage.IndexRoots) != 0 {
				t.Fatal("authority failure leaked root")
			}
			lockEmpty(t, e)
		})
	}
	// Same exact identity with a compatible revision remains admissible.
	t.Run("compatible-revision", func(t *testing.T) {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(n INT)")
		sessionSQL(t, a, "INSERT INTO t VALUES(1)")
		at := transactionBegin(t, a)
		sessionSQL(t, a, "INSERT INTO t VALUES(2)")
		bt := transactionBegin(t, b)
		done := transactionalIndexAsync(t, e, b, "CREATE INDEX idx ON t(n)")
		lockPending(t, e, 1)
		base := e.CatalogRead()
		ref := roleTableRef(t, e, "t")
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
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
		transactionEnd(t, a, at, false)
		lockAwait(t, done, nil)
		transactionEnd(t, b, bt, true)
		btreeWantKeys(t, e, "idx", "1")
		lockEmpty(t, e)
	})
}
