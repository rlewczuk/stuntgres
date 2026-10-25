package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func ownershipUnitCapture(t *testing.T, e *Engine, stage *CatalogStage, journal *session.Journal) {
	t.Helper()
	e.Mu.Lock()
	err := e.captureCatalogStageLocked(stage, journal, e.PrivateCatalogLocked(journal).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, journal)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRoleOwnershipTransactionalRoleOverlay(t *testing.T) {
	e, first, ns := ownershipCleanupStage(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	op.Session.Transactions.RetainedMessage = true
	defer func() { op.Session.Transactions.RetainedMessage = false; op.Session.Transactions.Catalog = nil }()
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	first.AuthorizeLocked(auth, op.Session)
	base, state := e.RoleStore.Published, e.Catalog.State
	baseEdges := append([]authorization.Membership(nil), fixtureEdges(base)...)
	bootstrap := auth.Roles.LookupRef("postgres")
	source, target, reader := auth.Roles.LookupRef("ownership_source"), auth.Roles.LookupRef("ownership_target"), auth.Roles.LookupRef("ownership_reader")
	monitor := auth.Roles.LookupRef("pg_monitor")
	wantSeed := []authorization.Membership{
		{Role: auth.Roles.LookupRef("pg_read_all_settings"), Member: monitor, Grantor: bootstrap, Inherit: true, Set: true},
		{Role: auth.Roles.LookupRef("pg_read_all_stats"), Member: monitor, Grantor: bootstrap, Inherit: true, Set: true},
		{Role: auth.Roles.LookupRef("pg_stat_scan_tables"), Member: monitor, Grantor: bootstrap, Inherit: true, Set: true},
	}
	survivor := authorization.Membership{Role: target, Member: source, Grantor: bootstrap, Admin: true}
	wantFirst := append(append([]authorization.Membership(nil), wantSeed...), survivor)
	wantBase := append(append([]authorization.Membership(nil), wantFirst...), authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true})
	if !reflect.DeepEqual(baseEdges, wantBase) {
		t.Fatal("canonical predefined/fixture seed membership mismatch", baseEdges, wantBase)
	}
	journal := session.NewJournal()
	ownershipUnitCapture(t, e, first, journal)
	if journal.Roles != first.RoleCandidateRecord || journal.RolesBase != base {
		t.Fatal("stage lost private role candidate or original committed predecessor")
	}
	if e.RoleStore.Published != base || e.Catalog.State != state || !reflect.DeepEqual(fixtureEdges(base), baseEdges) {
		t.Fatal("capture published or mutated retained authority")
	}
	if !reflect.DeepEqual(fixtureEdges(journal.Roles), wantFirst) {
		t.Fatal("cleanup changed surviving provenance or retained deleted grantor edge", fixtureEdges(journal.Roles), wantFirst)
	}
	mark := journal.Clone()
	op.Session.Transactions.Catalog = journal
	// An ordinary descriptor-only stage must preserve the already pending graph.
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(journal)
	e.Mu.Unlock()
	nilCandidate := e.BeginCatalogStage(private)
	replacement := *private.MustNamespace(ns.Address())
	replacement.ObjectRevision++
	if err := nilCandidate.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	ownershipUnitCapture(t, e, nilCandidate, journal)
	if journal.Roles != first.RoleCandidateRecord || journal.RolesBase != base || mark.Descriptors[ns.Address()] == journal.Descriptors[ns.Address()] {
		t.Fatal("nil candidate erased graph or modified marked descriptors")
	}
	// Stage a second deletion against the private predecessor, not base.Revision.
	auth, err = OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	second := e.ownershipStage(auth, op)
	reader = auth.Roles.LookupRef("ownership_reader")
	if err := role.StageOwnedCleanup(context.Background(), second, auth.Roles, auth.RoleRecord, map[authorization.PrincipalRef]bool{reader: true}); err != nil {
		t.Fatal(err)
	}
	// A source with no outgoing grant can have a nil second candidate; construct
	// a genuine further deletion of an existing surviving edge for merge proof.
	if second.RoleCandidateRecord == nil {
		next := *auth.RoleRecord
		edges := fixtureEdges(auth.RoleRecord)
		kept := make([]authorization.Membership, 0, len(edges)-1)
		removed := 0
		for _, edge := range edges {
			if edge == survivor {
				removed++
				continue
			}
			kept = append(kept, edge)
		}
		if removed != 1 {
			t.Fatal("missing independent surviving fixture membership", edges)
		}
		setFixtureEdges(&next, kept)
		next.Revision++
		second.RoleCandidateRecord = &next
	}
	if err := second.validateRoleCandidate(auth.RoleRecord); err != nil {
		t.Fatal("private predecessor rejected", err)
	}
	if err := second.validateRoleCandidate(base); err == nil {
		t.Fatal("committed root incorrectly accepted as private predecessor")
	}
	if _, err := e.ValidateCatalogStage(context.Background(), second); err != nil {
		t.Fatal("ordinary stage validation used committed rather than private roles", err)
	}
	child := journal.Clone()
	ownershipUnitCapture(t, e, second, child)
	if child.Roles != second.RoleCandidateRecord || child.RolesBase != base || journal.Roles != first.RoleCandidateRecord || mark.Roles != first.RoleCandidateRecord {
		t.Fatal("second candidate merge or savepoint clone corrupted roots")
	}
	if !reflect.DeepEqual(fixtureEdges(child.Roles), wantSeed) || !reflect.DeepEqual(fixtureEdges(mark.Roles), wantFirst) {
		t.Fatal("private/marked exact membership provenance changed", fixtureEdges(child.Roles), fixtureEdges(mark.Roles))
	}
	e.Mu.Lock()
	err = e.CatalogPreflightLocked(child)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("unchanged original committed predecessor rejected", err)
	}
	nondeletion := *auth.RoleRecord
	survivors := append([]authorization.Membership(nil), fixtureEdges(auth.RoleRecord)...)
	survivors[0].Set = !survivors[0].Set
	setFixtureEdges(&nondeletion, survivors)
	nondeletion.Revision++
	second.RoleCandidateRecord = &nondeletion
	if err := second.validateRoleCandidate(auth.RoleRecord); err == nil {
		t.Fatal("nondeletion membership candidate accepted")
	}
	second.RoleCandidateRecord = child.Roles
	bad := *second.RoleCandidateRecord
	bad.Revision++
	second.RoleCandidateRecord = &bad
	if err := second.validateRoleCandidate(auth.RoleRecord); err == nil {
		t.Fatal("invalid candidate revision accepted")
	}
	exhausted := *auth.RoleRecord
	exhausted.Revision = ^uint64(0)
	second.RoleRevision = exhausted.Revision
	if err := second.validateRoleCandidate(&exhausted); err == nil {
		t.Fatal("revision exhaustion accepted")
	}
	if e.RoleStore.Published != base || e.Catalog.State != state {
		t.Fatal("private candidate work escaped into committed roots")
	}
	// A legitimate read refresh merges the exact private deletion delta into
	// the new committed role root by cloning the journal, never mutating marks.
	privateRoles, privateRevision := child.Roles, child.Roles.Revision
	op.Session.Transactions.Catalog = child
	ownershipUnitExec(t, e, "COMMENT ON ROLE ownership_target IS 'ownership overlay peer'")
	peerRoles := e.RoleStore.Published
	e.Mu.Lock()
	op.Session.Mu.Lock()
	err = e.refreshOperationRolesLocked(op)
	refreshed := op.Session.Transactions.Catalog
	op.Session.Mu.Unlock()
	e.Mu.Unlock()
	if err != nil || refreshed == child || refreshed.Roles == privateRoles || refreshed.RolesBase != peerRoles ||
		!reflect.DeepEqual(fixtureEdges(refreshed.Roles), wantSeed) ||
		fixtureRole(refreshed.Roles, target).Comment != "ownership overlay peer" {
		t.Fatal("immutable role refresh failed exact-delta merge", err)
	}
	if child.Roles != privateRoles || child.RolesBase != base || privateRoles.Revision != privateRevision ||
		!reflect.DeepEqual(fixtureEdges(privateRoles), wantSeed) || !reflect.DeepEqual(fixtureEdges(mark.Roles), wantFirst) ||
		mark.RolesBase != base || !reflect.DeepEqual(fixtureEdges(base), baseEdges) ||
		fixtureRole(base, target).Comment != "" || fixtureRole(privateRoles, target).Comment != "" ||
		e.RoleStore.Published != peerRoles || e.Catalog.State != state {
		t.Fatal("role refresh mutated private/marked/committed predecessors")
	}
}

func TestRoleOwnershipTransactionalPublication(t *testing.T) {
	for _, veto := range []string{"cancel", "roles", "database", "reset", "invalid-member"} {
		t.Run(veto, func(t *testing.T) {
			e, stage, _ := ownershipCleanupStage(t)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			stage.AuthorizeLocked(auth, op.Session)
			journal := session.NewJournal()
			ownershipUnitCapture(t, e, stage, journal)
			if journal.Roles != stage.RoleCandidateRecord {
				t.Fatal("missing candidate before final publication preflight")
			}
			original := e.RoleStore.Published
			originalEdges := append([]authorization.Membership(nil), fixtureEdges(original)...)
			privateEdges := append([]authorization.Membership(nil), fixtureEdges(journal.Roles)...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch veto {
			case "cancel":
				cancel()
			case "roles":
				// Change the exact grantor-attributed edge cleanup deletes,
				// not merely the publication revision of an equivalent root.
				ownershipUnitExec(t, e, "GRANT ownership_target TO ownership_reader WITH SET FALSE GRANTED BY ownership_source")
			case "database":
				db := *e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
				db.ObjectRevision++
				next := catalog.CloneState(e.Catalog.State)
				next.SetDescriptor(db.Address(), &db)
				e.Catalog.PublishLocked(next)
			case "reset":
				if err := Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
			case "invalid-member":
				next := *stage.RoleCandidateRecord
				edges := append([]authorization.Membership(nil), fixtureEdges(&next)...)
				edges[0].Set = !edges[0].Set
				setFixtureEdges(&next, edges)
				stage.RoleCandidateRecord = &next
			}
			state, roles := e.Catalog.State, e.RoleStore.Published
			if veto == "invalid-member" {
				e.Mu.Lock()
				err = e.captureCatalogStageLocked(stage, journal.Clone(), e.PrivateCatalogLocked(journal).State(), nil)
				e.Mu.Unlock()
			} else {
				_, err = e.preparePrivateCatalog(ctx, journal, stage.Base.Epoch())
				if err == nil {
					e.Mu.Lock()
					err = e.CatalogPreflightLocked(journal)
					e.Mu.Unlock()
				}
			}
			if err == nil {
				t.Fatal("late invalid candidate/fence accepted")
			}
			if veto == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Catalog.State != state || e.RoleStore.Published != roles {
				t.Fatal("failed combined preparation published a prefix")
			}
			if !reflect.DeepEqual(fixtureEdges(original), originalEdges) || !reflect.DeepEqual(fixtureEdges(journal.Roles), privateEdges) {
				t.Fatal("failed preflight mutated committed/private predecessors")
			}
		})
	}
	for _, cancelCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "combined-commit", true: "combined-cancel"}[cancelCommit], func(t *testing.T) {
			e, unused, ns := ownershipCleanupStage(t)
			e.DiscardCatalogStage(unused)
			oldState, oldRoles := e.Catalog.State, e.RoleStore.Published
			oldEdges := append([]authorization.Membership(nil), fixtureEdges(oldRoles)...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := batchUnitStart(t, sessionStart(t, e, nil), ctx)
			defer message.end(context.Canceled)
			stmt := &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}
			if _, err := message.step(stmt); err != nil {
				t.Fatal("retained cleanup stage", err)
			}
			candidate := message.op.Session.Transactions.Catalog.Roles
			if candidate == nil || e.Catalog.State != oldState || e.RoleStore.Published != oldRoles {
				t.Fatal("retained cleanup lost candidate or published before message end")
			}
			privateEdges := append([]authorization.Membership(nil), fixtureEdges(candidate)...)
			privateRevision := candidate.Revision
			target := oldRoles.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_target")
			oldTarget := fixtureRole(oldRoles, target)
			other := e.BeginCatalogStage(e.CatalogRead())
			unrelated := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200001}, ObjectName: "ownership_combined_peer", ObjectRevision: 1, Owner: ns.Owner}
			if err := other.Create(unrelated); err != nil {
				t.Fatal(err)
			}
			ownershipUnitPublish(t, e, other)
			ownershipUnitExec(t, e, "COMMENT ON ROLE ownership_target IS 'ownership combined peer'")
			peerRoles := e.RoleStore.Published
			peerEdges := append([]authorization.Membership(nil), fixtureEdges(peerRoles)...)
			before := e.Catalog.State
			if cancelCommit {
				cancel()
			}
			err := message.end(nil)
			if cancelCommit {
				if !errors.Is(err, context.Canceled) || e.Catalog.State != before || e.RoleStore.Published != peerRoles {
					t.Fatal("canceled combined commit published a prefix", err)
				}
			} else {
				if err != nil || e.RoleStore.Published == candidate || len(e.Catalog.State.MustNamespace(ns.Address()).ACL.Entries) != 0 ||
					!reflect.DeepEqual(fixtureEdges(e.RoleStore.Published), privateEdges) {
					t.Fatal("combined commit lost catalog/role cleanup", err)
				}
			}
			if fixtureRole(e.RoleStore.Published, target).Comment != "ownership combined peer" ||
				fixtureRole(oldRoles, target) != oldTarget || fixtureRole(candidate, target) != oldTarget ||
				!reflect.DeepEqual(fixtureEdges(peerRoles), peerEdges) ||
				!reflect.DeepEqual(fixtureEdges(candidate), privateEdges) || candidate.Revision != privateRevision {
				t.Fatal("combined merge/cancel lost peer comment or mutated base/private/peer roots")
			}
			if e.Catalog.State.Get(unrelated.Address()) != unrelated || !reflect.DeepEqual(fixtureEdges(oldRoles), oldEdges) || oldState.Get(ns.Address()) != ns || len(ns.ACL.Entries) != 2 {
				t.Fatal("combined completion lost peer commit or mutated retained roots")
			}
		})
	}
	for _, kind := range []string{"type", "bulk"} {
		t.Run(kind+"-replacement-fence", func(t *testing.T) {
			e, _, source, target, _ := ownershipUnitRoles()
			name, class := "ownership_readmit_type", ids.TypeClassID
			create, drop, alter := "CREATE TYPE ownership_readmit_type AS ENUM('a')", "DROP TYPE ownership_readmit_type", "ALTER TYPE ownership_readmit_type OWNER TO ownership_source"
			if kind == "bulk" {
				name, class = "ownership_readmit_table", ids.RelationClassID
				create, drop, alter = "CREATE TABLE ownership_readmit_table(id INT)", "DROP TABLE ownership_readmit_table", "ALTER TABLE ownership_readmit_table OWNER TO ownership_source"
			}
			ownershipUnitExec(t, e, create)
			ownershipUnitExec(t, e, alter)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			key := ids.NameKey{ClassID: class, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}
			address := auth.Catalog.NameAddress(key)
			captured := auth.Catalog.Get(address)
			stage := e.ownershipStage(auth, op)
			defer e.DiscardCatalogStage(stage)
			e.Mu.Lock()
			if kind == "type" {
				err = role.StageOwnership(stage, captured, target)
			} else {
				path, _ := auth.ReferencePath()
				stmt := &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Target: parser.RoleSpec{Name: "ownership_target"}}
				plan, planErr := role.PlanOwned(auth.View, stmt, func(a ids.ObjectAddress) string { return stored.DependencyObject(auth.Catalog, a, path) })
				err = planErr
				if err == nil {
					err = role.StageOwned(RoleHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, plan)
				}
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			// This is the exact provider-predecessor change possible during the
			// unlocked admission window, not a fabricated scalar relation lock.
			ownershipUnitExec(t, e, drop)
			ownershipUnitExec(t, e, create)
			current, roles := e.Catalog.State, e.RoleStore.Published
			replacement := current.NameAddress(key)
			if replacement == address || replacement == (ids.ObjectAddress{}) {
				t.Fatal("replacement did not remint identity")
			}
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
				t.Fatal("captured ownership stage adopted same-name replacement")
			}
			if e.Catalog.State != current || e.RoleStore.Published != roles || auth.Catalog.Get(address) != captured || current.Get(address) != nil {
				t.Fatal("stale stage partially published or mutated retained provider")
			}
			owner, _, _ := access.DescriptorACL(captured)
			newOwner, _, _ := access.DescriptorACL(current.Get(replacement))
			if owner != source || newOwner == target {
				t.Fatal("stale owner stage retargeted replacement")
			}
		})
	}
	t.Run("policy-overlay", ownershipTransactionalPolicyOverlay)
}

func TestRoleOwnershipTransactionalUnrelatedCommit(t *testing.T) {
	e, stage, ns := ownershipCleanupStage(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage.AuthorizeLocked(auth, op.Session)
	journal := session.NewJournal()
	ownershipUnitCapture(t, e, stage, journal)
	other := e.BeginCatalogStage(e.CatalogRead())
	unrelated := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200001}, ObjectName: "ownership_unrelated", ObjectRevision: 1, Owner: ns.Owner}
	if err := other.Create(unrelated); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, other)
	committed, roles := e.Catalog.State, e.RoleStore.Published
	prepared, err := e.preparePrivateCatalog(context.Background(), journal, stage.Base.Epoch())
	if err != nil {
		t.Fatal("unrelated commit invalidated ownership journal", err)
	}
	if prepared.Get(unrelated.Address()) != unrelated || prepared.Get(ns.Address()) != stage.Get(ns.Address()) {
		t.Fatal("combined preparation lost unrelated or staged descriptors")
	}
	if journal.Roles != stage.RoleCandidateRecord || journal.RolesBase != roles {
		t.Fatal("combined preparation lost private role projection")
	}
	if e.Catalog.State != committed || e.RoleStore.Published != roles {
		t.Fatal("preparation installed roots early")
	}
	// RolesBase describes the exact cleanup delta, not a blanket publication
	// identity fence. A disjoint actual role comment must merge without writes
	// during preparation; changing the deleted membership's SET option must veto.
	baseEdges := append([]authorization.Membership(nil), fixtureEdges(roles)...)
	private := journal.Roles
	privateEdges := append([]authorization.Membership(nil), fixtureEdges(private)...)
	ownershipUnitExec(t, e, "COMMENT ON ROLE ownership_target IS 'ownership unrelated peer'")
	peerRoles := e.RoleStore.Published
	e.Mu.Lock()
	err = e.CatalogPreflightLocked(journal)
	e.Mu.Unlock()
	if err != nil || e.Catalog.State != committed || e.RoleStore.Published != peerRoles ||
		journal.RolesBase != roles || journal.Roles != private ||
		!reflect.DeepEqual(fixtureEdges(private), privateEdges) || !reflect.DeepEqual(fixtureEdges(roles), baseEdges) {
		t.Fatal("disjoint role publication failed exact-delta preflight or changed retained roots", err)
	}
	ownershipUnitExec(t, e, "GRANT ownership_target TO ownership_reader WITH SET FALSE GRANTED BY ownership_source")
	competing := e.RoleStore.Published
	competingEdges := append([]authorization.Membership(nil), fixtureEdges(competing)...)
	e.Mu.Lock()
	err = e.CatalogPreflightLocked(journal)
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("competing touched membership predecessor accepted")
	}
	if e.Catalog.State != committed || e.RoleStore.Published != competing || journal.RolesBase != roles || journal.Roles != private ||
		!reflect.DeepEqual(fixtureEdges(competing), competingEdges) || !reflect.DeepEqual(fixtureEdges(private), privateEdges) ||
		!reflect.DeepEqual(fixtureEdges(roles), baseEdges) || !reflect.DeepEqual(fixtureEdges(peerRoles), baseEdges) {
		t.Fatal("touched membership veto overwrote committed/private/peer roots")
	}
}

func ownershipTransactionalPolicyOverlay(t *testing.T) {
	e, owner, source, _, reader := ownershipUnitRoles()
	for _, q := range []string{
		"CREATE TABLE ownership_policy_provider(id INT)",
		"CREATE TABLE ownership_policy_probe(id INT)",
		"INSERT INTO ownership_policy_probe VALUES(7)",
		"GRANT SELECT ON ownership_policy_probe TO PUBLIC",
		"CREATE POLICY ownership_policy_a_mixed ON ownership_policy_probe TO ownership_source,ownership_reader,ownership_source USING(id>0 AND 'ownership_policy_provider'::regclass IS NOT NULL)",
		"CREATE POLICY ownership_policy_b_last ON ownership_policy_probe TO ownership_source USING(id>0)",
		"CREATE POLICY ownership_policy_c_public ON ownership_policy_probe TO PUBLIC USING(false)",
		"ALTER TABLE ownership_policy_probe ENABLE ROW LEVEL SECURITY,FORCE ROW LEVEL SECURITY",
	} {
		ownershipUnitExec(t, e, q)
	}
	base, roles := e.Catalog.State, e.RoleStore.Published
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_probe"})
	provider := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_provider"})
	original := base.MustRelation(address)
	unaffectedACL := original.Security.ACL
	unaffectedACL.Entries = append([]authorization.ACLEntry(nil), unaffectedACL.Entries...)
	wantPublic := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}
	publicEntries := 0
	for _, entry := range unaffectedACL.Entries {
		if entry.Grantee.Public {
			publicEntries++
			if entry != wantPublic {
				t.Fatal("independent PUBLIC SELECT grant", entry, wantPublic)
			}
		}
	}
	if !unaffectedACL.Explicit || publicEntries != 1 {
		t.Fatal("missing exact independent PUBLIC SELECT grant", unaffectedACL)
	}
	originals := append([]catalog.PolicyDefinition(nil), original.Security.Policies...)
	if len(originals) != 3 {
		t.Fatal("canonical policy fixture count")
	}
	for i := range originals {
		originals[i].Roles = append([]catalog.PolicyRole(nil), originals[i].Roles...)
		policy := originals[i]
		if policy.OID == 0 || !policy.Permissive || policy.Command != catalog.PolicyAll || policy.Using == nil || policy.Check != nil {
			t.Fatal("canonical policy identity/mode/expression", policy)
		}
	}
	mixed, last, public := originals[0], originals[1], originals[2]
	if mixed.Name != "ownership_policy_a_mixed" || last.Name != "ownership_policy_b_last" || public.Name != "ownership_policy_c_public" ||
		mixed.OID == last.OID || mixed.OID == public.OID || last.OID == public.OID {
		t.Fatal("canonical policy identities/order", originals)
	}
	if !reflect.DeepEqual(mixed.Roles, []catalog.PolicyRole{{Ref: source}, {Ref: reader}, {Ref: source}}) ||
		!reflect.DeepEqual(last.Roles, []catalog.PolicyRole{{Ref: source}}) ||
		!reflect.DeepEqual(public.Roles, []catalog.PolicyRole{{Public: true}}) {
		t.Fatal("canonical duplicate/last/PUBLIC targets", originals)
	}
	if !mixed.Using.DependsOn(provider) {
		t.Fatal("canonical policy provider binding")
	}
	presentation, err := stored.PolicySQL(context.Background(), e.CatalogRead(), stored.PolicyOf(mixed.Using))
	if err != nil {
		t.Fatal(err)
	}
	store := e.Storage.Relations[address]
	versions := store.Versions
	check := func(relation *catalog.Relation, afterReader bool) {
		t.Helper()
		if !reflect.DeepEqual(relation.Security.ACL, unaffectedACL) || !reflect.DeepEqual(original.Security.ACL, unaffectedACL) {
			t.Fatal("policy-only cleanup changed independently surviving ACL", relation.Security.ACL, unaffectedACL)
		}
		want := []catalog.PolicyDefinition{public}
		if !afterReader {
			projected := mixed
			projected.Roles = []catalog.PolicyRole{{Ref: reader}}
			want = []catalog.PolicyDefinition{projected, public}
		}
		if !reflect.DeepEqual(relation.Security.Policies, want) || !relation.Security.RLSEnabled || !relation.Security.RLSForced {
			t.Fatal("policy cleanup changed identities/order/flags/targets/expressions", relation.Security.Policies, want)
		}
		if !afterReader {
			current := relation.Security.Policies[0]
			if current.Using != mixed.Using || !current.Using.DependsOn(provider) {
				t.Fatal("policy cleanup replaced provider binding")
			}
			sql, err := stored.PolicySQL(context.Background(), e.CatalogRead(), stored.PolicyOf(current.Using))
			if err != nil || sql != presentation {
				t.Fatal("policy cleanup changed frozen expression/provider presentation", sql, presentation, err)
			}
		}
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := role.StageOwnedCleanup(context.Background(), stage, e.RoleStore.Read(&e.Catalog.Owner, stage.Base, roles), roles, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	journal := session.NewJournal()
	ownershipUnitCapture(t, e, stage, journal)
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(journal)
	e.Mu.Unlock()
	check(private.MustRelation(address), false)
	mark := journal.Clone()
	second := e.BeginCatalogStage(private)
	if err := role.StageOwnedCleanup(context.Background(), second, e.RoleStore.Read(&e.Catalog.Owner, private, roles), roles, map[authorization.PrincipalRef]bool{reader: true}); err != nil {
		t.Fatal(err)
	}
	child := journal.Clone()
	ownershipUnitCapture(t, e, second, child)
	e.Mu.Lock()
	next := e.PrivateCatalogLocked(child)
	marked := e.PrivateCatalogLocked(mark)
	e.Mu.Unlock()
	check(next.MustRelation(address), true)
	check(marked.MustRelation(address), false)
	if e.Catalog.State != base || e.RoleStore.Published != roles || !reflect.DeepEqual(original.Security.Policies, originals) {
		t.Fatal("private policy projection mutated committed/marked originals")
	}
	// Finish and combined commit carry the same policy-only projection through
	// the actual retained owner; the marked candidate above stays immutable.
	message := batchUnitStart(t, sessionStart(t, e, nil), context.Background())
	defer message.end(context.Canceled)
	for i, name := range []string{"ownership_source", "ownership_reader"} {
		if _, err := message.step(&parser.DropOwned{Sources: []parser.RoleSpec{{Name: name}}, Mode: parser.DropRestrict}); err != nil {
			t.Fatal("policy-only Finish", err)
		}
		e.Mu.Lock()
		pending := e.PrivateCatalogLocked(message.op.Session.Transactions.Catalog)
		e.Mu.Unlock()
		check(pending.MustRelation(address), i == 1)
		if e.Catalog.State != base || e.RoleStore.Published != roles {
			t.Fatal("policy-only Finish published before outer end")
		}
	}
	if err := message.end(nil); err != nil {
		t.Fatal("policy-only combined commit", err)
	}
	check(e.Catalog.State.MustRelation(address), true)
	if e.RoleStore.Published != roles || e.Storage.Relations[address] != store || len(versions) != 1 || len(store.Versions) != 1 ||
		&store.Versions[0] != &versions[0] || !reflect.DeepEqual(original.Security.Policies, originals) {
		t.Fatal("policy-only commit changed role/storage or retained policy roots")
	}
	e.Mu.Lock()
	marked = e.PrivateCatalogLocked(mark)
	e.Mu.Unlock()
	check(marked.MustRelation(address), false)
}
