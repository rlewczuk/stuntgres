package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Public policy behavior belongs to the shared reference definitions. This guard
// inspects the actual immutable policy identity and owner publication roots.
func TestDriverBatchPolicyIdentityPublication(t *testing.T) {
	for _, disposition := range []string{"commit", "late_failure", "cascade_failure"} {
		t.Run(disposition, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE policy_owner(id INT)")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE policy_dependency")
			ownerRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "policy_owner")
			dependency := enginetest.CatalogRef(t, e, e.CatalogRead(), "policy_dependency")
			base, frontier := e.Catalog.State, e.Visibility.CommitSeq()
			owner := e.Catalog.State.MustRelation(ownerRef.Address)
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			var oid ids.OID
			var private *catalog.Relation
			for i, q := range []string{
				"CREATE POLICY original ON policy_owner USING(nextval('policy_dependency')>0)",
				"ALTER POLICY original ON policy_owner USING(id>0) WITH CHECK(nextval('policy_dependency')>0)",
				"ALTER POLICY original ON policy_owner RENAME TO renamed",
			} {
				if _, err := m.step(enginetest.Statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
				private = s.Transactions.Catalog.Descriptors[ownerRef.Address].(*catalog.Relation)
				if len(private.Security.Policies) != 1 || private.Security.Policies[0].OID == 0 {
					t.Fatal("missing actual private policy identity", private.Security.Policies)
				}
				if i == 0 {
					oid = private.Security.Policies[0].OID
				} else if private.Security.Policies[0].OID != oid {
					t.Fatal("ALTER/rename allocated another policy identity", private.Security.Policies[0].OID, oid)
				}
				if e.Catalog.State != base || e.Visibility.CommitSeq() != frontier || e.Catalog.State.Get(ownerRef.Address) != owner || len(owner.Security.Policies) != 0 {
					t.Fatal("private policy escaped before owner publication")
				}
			}
			if private.Security.Policies[0].Name != "renamed" || private.Security.Policies[0].Using == nil || private.Security.Policies[0].Check == nil {
				t.Fatal("ALTER/rename lost private policy definition", private.Security.Policies)
			}
			e.Mu.Lock()
			view := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			stage := e.BeginCatalogStage(view)
			defer e.DiscardCatalogStage(stage)
			closure, err := engine.DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{dependency.Address})
			if err != nil {
				t.Fatal(err)
			}
			if len(closure) != 1 || closure[0].Automatic || closure[0].Label != "policy renamed on table policy_owner" {
				t.Fatal("private altered policy dependency closure lost identity", closure)
			}
			if err = engine.StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{dependency.Address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			pruned, ok := stage.Get(ownerRef.Address).(*catalog.Relation)
			if !ok || len(pruned.Security.Policies) != 0 || len(private.Security.Policies) != 1 || private.Security.Policies[0].OID != oid || len(owner.Security.Policies) != 0 {
				t.Fatal("closure mutated immutable private or committed policy root")
			}
			if disposition == "commit" {
				if err := m.end(nil); err != nil {
					t.Fatal(err)
				}
				published := e.Catalog.State.MustRelation(ownerRef.Address)
				if published != private || len(published.Security.Policies) != 1 || published.Security.Policies[0].OID != oid || published.Security.Policies[0].Name != "renamed" || e.Visibility.CommitSeq() != frontier+1 || len(owner.Security.Policies) != 0 {
					t.Fatal("true end did not retain actual policy identity")
				}
				return
			}
			if disposition == "cascade_failure" {
				if _, err := m.step(enginetest.Statement(t, "DROP SEQUENCE policy_dependency CASCADE")); err != nil {
					t.Fatal(err)
				}
				candidate := s.Transactions.Catalog.Descriptors[ownerRef.Address].(*catalog.Relation)
				if len(candidate.Security.Policies) != 0 || s.Transactions.Catalog.Descriptors[dependency.Address] != nil || e.Catalog.State != base || e.Catalog.State.Get(dependency.Address) == nil {
					t.Fatal("private cascade omitted policy pruning or published early")
				}
			}
			_, err = m.step(enginetest.Statement(t, "SELECT missing"))
			if err == nil || m.end(err) != err || e.Catalog.State != base || e.Visibility.CommitSeq() != frontier || e.Catalog.State.Get(ownerRef.Address) != owner || len(owner.Security.Policies) != 0 || e.Catalog.State.Get(dependency.Address) == nil {
				t.Fatal("late failure published policy/dependency roots", err)
			}
		})
	}
}
