package engine

import (
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"testing"
)

func TestRuleViewSourceCollectionAuthority(t *testing.T) {
	for _, shape := range []string{"invoker", "definer_over_invoker", "invoker_over_definer", "definer"} {
		t.Run(shape, func(t *testing.T) {
			e, _, base, inner := ruleUnitCatalog(t)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := operationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			actor, ok := auth.Roles.Lookup("pg_monitor")
			if !ok {
				t.Fatal("missing actor role")
			}
			owner, ok := auth.Roles.Lookup("pg_signal_backend")
			if !ok {
				t.Fatal("missing view owner role")
			}
			bootstrap := auth.State.CurrentUser
			base.Owner = bootstrap
			grant := func(role authorization.PrincipalRef) authorization.ACLValue {
				return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: role}, Privileges: authorization.SelectPrivilege, Grantor: bootstrap}}}
			}
			baseRef := ruleUnitRef(e, base)
			inner.Owner, inner.RelationKind = owner.Ref, catalog.ViewRelation
			inner.Security.ACL = grant(actor.Ref)
			inner.Security.ACL.Entries = append(inner.Security.ACL.Entries, grant(owner.Ref).Entries...)
			inner.View = stored.NewView(stored.ViewParts{
				Query:   &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(base.Columns), Ref: baseRef}},
				Invoker: shape == "invoker" || shape == "definer_over_invoker",
				Sources: []stored.ViewLeaf{{Ref: baseRef, Reads: []int32{1}, Attributes: []int32{1, 2}}},
			})
			root := inner
			if shape == "definer_over_invoker" || shape == "invoker_over_definer" {
				outer := *inner
				outer.ObjectAddress.ObjectID++
				outer.ObjectName = "outer_view"
				outer.View = stored.NewView(stored.ViewParts{
					Query:   &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(inner.Columns), Ref: ruleUnitRef(e, inner)}},
					Invoker: shape == "invoker_over_definer",
					Sources: []stored.ViewLeaf{{Ref: ruleUnitRef(e, inner), Reads: []int32{1}, Attributes: []int32{1, 2}}},
				})
				next := catalog.CloneState(e.catalog.state)
				next.SetDescriptor(outer.Address(), &outer)
				e.catalog.state = next
				root = &outer
			}
			// A nested invoker retains the ORIGINAL source invoker even below
			// a definer. A nested definer still reads its base as its own owner.
			callerReads := shape == "invoker" || shape == "definer_over_invoker"
			reader, other := owner.Ref, actor.Ref
			if callerReads {
				reader, other = actor.Ref, owner.Ref
			}
			base.Security.ACL = grant(reader)
			auth.Recapture(e.catalog.read(e.catalog.state))
			auth.State.CurrentUser = owner.Ref
			leaf := exec.Leaf{Ref: ruleUnitRef(e, root), Relation: root, Principal: actor.Ref}
			collect := func(view AuthorizationView, invoker authorization.PrincipalRef, leaves ...exec.Leaf) ([]ids.ObjectRef, error) {
				t.Helper()
				e.mu.Lock()
				defer e.mu.Unlock()
				return e.queryTableRefsLocked(view, invoker, leaves, nil, ids.SessionToken{}, plan.RowMark{})
			}
			assertCollected := func(invoker authorization.PrincipalRef, leaves ...exec.Leaf) {
				t.Helper()
				refs, err := collect(auth, invoker, leaves...)
				if err != nil || !reflect.DeepEqual(refs, []ids.ObjectRef{baseRef}) {
					t.Fatalf("canonical collection lost source authority or physical closure: refs=%v err=%v", refs, err)
				}
			}
			assertCollected(owner.Ref, leaf)
			fallback := leaf
			fallback.Principal = authorization.PrincipalRef{}
			assertCollected(actor.Ref, fallback)
			if callerReads {
				// The same executable view appears under two different principals;
				// collecting the first must not permission-cache the second.
				refs, err := collect(auth, owner.Ref, leaf, fallback)
				defaultUnitDiagnostic(t, err, "42501", "permission denied for table src")
				if len(refs) != 0 {
					t.Fatal("denied occurrence published a physical closure", refs)
				}
				fallback.AnalysisOnly = true
				assertCollected(owner.Ref, leaf, fallback)
			} else {
				// Supplied or retained invokers cannot replace a definer's owner.
				assertCollected(owner.Ref, fallback)
			}
			// Capture a genuinely new catalog ACL snapshot. Collection must use
			// that snapshot, while an already paired snapshot remains unchanged.
			revoked := *base
			revoked.Security.ACL = grant(other)
			next := catalog.CloneState(e.catalog.state)
			next.SetDescriptor(base.Address(), &revoked)
			current := auth
			current.Recapture(e.catalog.read(next))
			refs, err := collect(current, owner.Ref, leaf)
			defaultUnitDiagnostic(t, err, "42501", "permission denied for table src")
			if len(refs) != 0 {
				t.Fatal("current grant denial published a physical closure", refs)
			}
			assertCollected(owner.Ref, leaf)
			if leaf.Principal != actor.Ref || fallback.Principal != (authorization.PrincipalRef{}) {
				t.Fatal("collection changed caller-owned source recipes")
			}
		})
	}
}
