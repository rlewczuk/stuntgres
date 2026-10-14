package engine

import (
	"reflect"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestRuleViewSourceCollectionAuthority(t *testing.T) {
	for _, shape := range []string{"invoker", "definer_over_invoker", "invoker_over_definer", "definer"} {
		t.Run(shape, func(t *testing.T) {
			e, _, base, inner := ruleUnitCatalog(t)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := op.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			actor, ok := auth.roles.Lookup("pg_monitor")
			if !ok {
				t.Fatal("missing actor role")
			}
			owner, ok := auth.roles.Lookup("pg_signal_backend")
			if !ok {
				t.Fatal("missing view owner role")
			}
			bootstrap := auth.state.CurrentUser
			base.owner = bootstrap
			grant := func(role authorization.PrincipalRef) authorization.ACLValue {
				return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: role}, Privileges: authorization.SelectPrivilege, Grantor: bootstrap}}}
			}
			baseRef := ruleUnitRef(e, base)
			inner.owner, inner.relationKind = owner.Ref, viewRelation
			inner.acl = grant(actor.Ref)
			inner.acl.Entries = append(inner.acl.Entries, grant(owner.Ref).Entries...)
			inner.view = &viewDefinition{invoker: shape == "invoker" || shape == "definer_over_invoker", sources: []viewLeaf{{ref: baseRef, reads: []int32{1}, attributes: []int32{1, 2}}}}
			inner.view.query = &BoundQuery{Root: &sourceNode{querySchema: querySchema(base.columns), ref: baseRef}}
			root := inner
			if shape == "definer_over_invoker" || shape == "invoker_over_definer" {
				outer := *inner
				outer.address.ObjectID++
				outer.name = "outer_view"
				outer.view = &viewDefinition{invoker: shape == "invoker_over_definer", sources: []viewLeaf{{ref: ruleUnitRef(e, inner), reads: []int32{1}, attributes: []int32{1, 2}}}}
				outer.view.query = &BoundQuery{Root: &sourceNode{querySchema: querySchema(inner.columns), ref: ruleUnitRef(e, inner)}}
				next := cloneCatalog(e.state)
				next.descriptors[outer.address] = &outer
				e.state = next
				root = &outer
			}
			// A nested invoker retains the ORIGINAL source invoker even below
			// a definer. A nested definer still reads its base as its own owner.
			callerReads := shape == "invoker" || shape == "definer_over_invoker"
			reader, other := owner.Ref, actor.Ref
			if callerReads {
				reader, other = actor.Ref, owner.Ref
			}
			base.acl = grant(reader)
			auth.recaptureCatalog(CatalogRead{owner: e, state: e.state})
			auth.state.CurrentUser = owner.Ref
			leaf := queryLeaf{ref: ruleUnitRef(e, root), relation: root, principal: actor.Ref}
			collect := func(view AuthorizationView, invoker authorization.PrincipalRef, leaves ...queryLeaf) ([]ObjectRef, error) {
				t.Helper()
				e.mu.Lock()
				defer e.mu.Unlock()
				return e.queryTableRefsLocked(view, invoker, leaves, nil, SessionToken{}, queryRowMark{})
			}
			assertCollected := func(invoker authorization.PrincipalRef, leaves ...queryLeaf) {
				t.Helper()
				refs, err := collect(auth, invoker, leaves...)
				if err != nil || !reflect.DeepEqual(refs, []ObjectRef{baseRef}) {
					t.Fatalf("canonical collection lost source authority or physical closure: refs=%v err=%v", refs, err)
				}
			}
			assertCollected(owner.Ref, leaf)
			fallback := leaf
			fallback.principal = authorization.PrincipalRef{}
			assertCollected(actor.Ref, fallback)
			if callerReads {
				// The same executable view appears under two different principals;
				// collecting the first must not permission-cache the second.
				refs, err := collect(auth, owner.Ref, leaf, fallback)
				defaultUnitDiagnostic(t, err, "42501", "permission denied for table src")
				if len(refs) != 0 {
					t.Fatal("denied occurrence published a physical closure", refs)
				}
				fallback.analysisOnly = true
				assertCollected(owner.Ref, leaf, fallback)
			} else {
				// Supplied or retained invokers cannot replace a definer's owner.
				assertCollected(owner.Ref, fallback)
			}
			// Capture a genuinely new catalog ACL snapshot. Collection must use
			// that snapshot, while an already paired snapshot remains unchanged.
			revoked := *base
			revoked.acl = grant(other)
			next := cloneCatalog(e.state)
			next.descriptors[base.address] = &revoked
			current := auth
			current.recaptureCatalog(CatalogRead{owner: e, state: next})
			refs, err := collect(current, owner.Ref, leaf)
			defaultUnitDiagnostic(t, err, "42501", "permission denied for table src")
			if len(refs) != 0 {
				t.Fatal("current grant denial published a physical closure", refs)
			}
			assertCollected(owner.Ref, leaf)
			if leaf.principal != actor.Ref || fallback.principal != (authorization.PrincipalRef{}) {
				t.Fatal("collection changed caller-owned source recipes")
			}
		})
	}
}
