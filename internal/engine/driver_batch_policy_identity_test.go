package engine

import (
	"context"
	"stuntgres/internal/parser"
	"testing"
)

// Public policy behavior belongs to the shared reference definitions. This guard
// inspects the actual immutable policy identity and owner publication roots.
func TestDriverBatchPolicyIdentityPublication(t *testing.T) {
	for _, disposition := range []string{"commit", "late_failure", "cascade_failure"} {
		t.Run(disposition, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE policy_owner(id INT)")
			sessionSQL(t, s, "CREATE SEQUENCE policy_dependency")
			ownerRef := catalogRef(t, e, e.CatalogRead(), "policy_owner")
			dependency := catalogRef(t, e, e.CatalogRead(), "policy_dependency")
			base, frontier := e.state, e.commitSeq
			owner := e.state.descriptors[ownerRef.Address].(*relationDescriptor)
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			var oid OID
			var private *relationDescriptor
			for i, q := range []string{
				"CREATE POLICY original ON policy_owner USING(nextval('policy_dependency')>0)",
				"ALTER POLICY original ON policy_owner USING(id>0) WITH CHECK(nextval('policy_dependency')>0)",
				"ALTER POLICY original ON policy_owner RENAME TO renamed",
			} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
				private = s.transactions.catalog.descriptors[ownerRef.Address].(*relationDescriptor)
				if len(private.policies) != 1 || private.policies[0].OID == 0 {
					t.Fatal("missing actual private policy identity", private.policies)
				}
				if i == 0 {
					oid = private.policies[0].OID
				} else if private.policies[0].OID != oid {
					t.Fatal("ALTER/rename allocated another policy identity", private.policies[0].OID, oid)
				}
				if e.state != base || e.commitSeq != frontier || e.state.descriptors[ownerRef.Address] != owner || len(owner.policies) != 0 {
					t.Fatal("private policy escaped before owner publication")
				}
			}
			if private.policies[0].Name != "renamed" || private.policies[0].using == nil || private.policies[0].check == nil {
				t.Fatal("ALTER/rename lost private policy definition", private.policies)
			}
			e.mu.Lock()
			view := e.privateCatalogLocked(s.transactions.catalog)
			e.mu.Unlock()
			stage := e.BeginCatalogStage(view)
			defer e.DiscardCatalogStage(stage)
			closure, err := domainDependencyClosure(context.Background(), stage, []ObjectAddress{dependency.Address})
			if err != nil {
				t.Fatal(err)
			}
			if len(closure) != 1 || closure[0].automatic || closure[0].label != "policy renamed on table policy_owner" {
				t.Fatal("private altered policy dependency closure lost identity", closure)
			}
			if err = stageExpressionDependencies(context.Background(), stage, []ObjectAddress{dependency.Address}, parser.DropCascade, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			pruned, ok := stage.descriptors[ownerRef.Address].(*relationDescriptor)
			if !ok || len(pruned.policies) != 0 || len(private.policies) != 1 || private.policies[0].OID != oid || len(owner.policies) != 0 {
				t.Fatal("closure mutated immutable private or committed policy root")
			}
			if disposition == "commit" {
				if err := m.end(nil); err != nil {
					t.Fatal(err)
				}
				published := e.state.descriptors[ownerRef.Address].(*relationDescriptor)
				if published != private || len(published.policies) != 1 || published.policies[0].OID != oid || published.policies[0].Name != "renamed" || e.commitSeq != frontier+1 || len(owner.policies) != 0 {
					t.Fatal("true end did not retain actual policy identity")
				}
				return
			}
			if disposition == "cascade_failure" {
				if _, err := m.step(statement(t, "DROP SEQUENCE policy_dependency CASCADE")); err != nil {
					t.Fatal(err)
				}
				candidate := s.transactions.catalog.descriptors[ownerRef.Address].(*relationDescriptor)
				if len(candidate.policies) != 0 || s.transactions.catalog.descriptors[dependency.Address] != nil || e.state != base || e.state.descriptors[dependency.Address] == nil {
					t.Fatal("private cascade omitted policy pruning or published early")
				}
			}
			_, err = m.step(statement(t, "SELECT missing"))
			if err == nil || m.end(err) != err || e.state != base || e.commitSeq != frontier || e.state.descriptors[ownerRef.Address] != owner || len(owner.policies) != 0 || e.state.descriptors[dependency.Address] == nil {
				t.Fatal("late failure published policy/dependency roots", err)
			}
		})
	}
}
