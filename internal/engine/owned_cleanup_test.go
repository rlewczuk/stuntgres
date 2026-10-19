package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDropOwnedACLSelectiveCascade(t *testing.T) {
	for _, mask := range []authorization.Privileges{authorization.SelectPrivilege, authorization.UsagePrivilege, authorization.CreatePrivilege} {
		for _, alternate := range []bool{false, true} {
			e, owner, source, target, reader := ownershipUnitRoles()
			entries := []authorization.ACLEntry{objectACLUnitEntry(owner, source, mask, mask), objectACLUnitEntry(owner, target, mask, mask), objectACLUnitEntry(source, reader, mask, mask), objectACLUnitEntry(reader, target, mask, 0)}
			if alternate {
				entries = append(entries, objectACLUnitEntry(target, reader, mask, mask))
			}
			old := authorization.ACLValue{Explicit: true, Entries: entries}
			retained := append([]authorization.ACLEntry(nil), entries...)
			got, changed, err := authorization.OwnedACL(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), old, owner, map[authorization.PrincipalRef]bool{source: true})
			if err != nil || !changed || !reflect.DeepEqual(old.Entries, retained) {
				t.Fatal("cleanup/error/immutable source", got, changed, err)
			}
			readerGrant := false
			for _, entry := range got.Entries {
				if entry.Grantor == source || !entry.Grantee.Public && entry.Grantee.Role == source {
					t.Fatal("source ACL provenance survives", entry)
				}
				readerGrant = readerGrant || entry.Grantor == reader
			}
			if readerGrant != alternate {
				t.Fatal("alternate independent grant-option support", mask, alternate, got)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := authorization.OwnedACL(ctx, e.roleRead(e.catalog.read(e.catalog.state), e.roles), old, owner, map[authorization.PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
func TestDropOwnedPolicyTargets(t *testing.T) {
	_, _, source, target, _ := ownershipUnitRoles()
	old := []catalog.PolicyDefinition{{Name: "mixed", Roles: []catalog.PolicyRole{{Ref: source}, {Ref: target}, {Ref: source}}}, {Name: "last", Roles: []catalog.PolicyRole{{Ref: source}, {Ref: source}}}, {Name: "public", Roles: []catalog.PolicyRole{{Public: true}, {Ref: source}}}}
	before := []int{len(old[0].Roles), len(old[1].Roles), len(old[2].Roles)}
	got, changed := ownedPolicies(old, map[authorization.PrincipalRef]bool{source: true})
	if !changed || len(got) != 2 || got[0].Name != "mixed" || len(got[0].Roles) != 1 || got[0].Roles[0].Ref != target || got[1].Name != "public" || len(got[1].Roles) != 1 || !got[1].Roles[0].Public {
		t.Fatal(got, changed)
	}
	for i, n := range before {
		if len(old[i].Roles) != n {
			t.Fatal("policy input mutated", old)
		}
	}
}
func TestDropOwnedMembershipProvenance(t *testing.T) {
	e, owner, source, target, reader := ownershipUnitRoles()
	setFixtureEdges(e.roles, append(fixtureEdges(e.roles), authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true}, authorization.Membership{Role: target, Member: reader, Grantor: source, Admin: true, Inherit: true, Set: true}, authorization.Membership{Role: target, Member: owner, Grantor: reader, Inherit: true}))
	old := e.roles
	retained := append([]authorization.Membership(nil), fixtureEdges(old)...)
	next, err := ownedMembershipCandidate(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), old), old.revision, map[authorization.PrincipalRef]bool{source: true})
	if err != nil || next == nil || next.revision != old.revision+1 || !reflect.DeepEqual(fixtureEdges(old), retained) {
		t.Fatal(next, err)
	}
	for _, edge := range fixtureEdges(next) {
		if edge.Grantor == source || edge.Grantor == reader {
			t.Fatal("unsupported downstream membership survived", edge)
		}
	}
	if next.root.Read(authorization.PrincipalRef{}).RoleCount() != old.root.Read(authorization.PrincipalRef{}).RoleCount() || fixtureRole(next, source) != fixtureRole(old, source) {
		t.Fatal("DROP OWNED removed role endpoint")
	}
	if noChange, err := ownedMembershipCandidate(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), old), old.revision, map[authorization.PrincipalRef]bool{target: true}); err != nil || noChange != nil {
		t.Fatal("unchanged membership cloned/revised", noChange, err)
	}
}
func TestDropOwnedUnionRestrictAndCascade(t *testing.T) {
	e, _, source, _, _ := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_parent(id INT)", "CREATE TABLE ownership_child(id INT)", "INSERT INTO ownership_parent VALUES(7)", "CREATE VIEW ownership_external AS SELECT id FROM ownership_parent"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	setup := e.BeginCatalogStage(base)
	for _, name := range []string{"ownership_parent", "ownership_child"} {
		old := base.MustRelation(base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}))
		next := *old
		next.Owner = source
		next.ObjectRevision++
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	oldState, oldRoles := e.catalog.state, e.roles
	parent := oldState.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ownership_parent"})
	store := e.storage.relations[parent]
	rows := store.versions
	stmt := &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}
	_, err := testExecute(e, context.Background(), stmt, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop desired object(s) because other objects depend on them" {
		t.Fatal("actual external refusal", err)
	}
	if e.catalog.state != oldState || e.roles != oldRoles || e.storage.relations[parent] != store || &store.versions[0] != &rows[0] {
		t.Fatal("RESTRICT partially published")
	}
	stmt.Mode = parser.DropCascade
	if _, err = testExecute(e, context.Background(), stmt, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ownership_parent", "ownership_child", "ownership_external"} {
		if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}); exists {
			t.Fatal("actual cascade retained object", name)
		}
	}
	if _, exists := e.Roles().Find(source); !exists || oldState.Get(parent) == nil || len(rows) != 1 {
		t.Fatal("role/retained snapshot lost")
	}
}
func TestReassignOwnedSourceSetAndUnrelatedGrant(t *testing.T) {
	e, owner, source, target, reader := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_move(id INT)", "CREATE TABLE ownership_other(id INT)", "INSERT INTO ownership_move VALUES(7)"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	setup := e.BeginCatalogStage(base)
	move := base.MustRelation(base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ownership_move"}))
	next := *move
	next.Owner = source
	next.ObjectRevision++
	if err := setup.Replace(&next); err != nil {
		t.Fatal(err)
	}
	other := base.MustRelation(base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ownership_other"}))
	grant := *other
	grant.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.SelectPrivilege, authorization.SelectPrivilege), objectACLUnitEntry(source, reader, authorization.SelectPrivilege, 0)}}
	grant.ObjectRevision++
	if err := setup.Replace(&grant); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, setup)
	before := e.catalog.state
	store := e.storage.relations[move.Address()]
	roles := e.roles
	late := &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}, {Name: "absent"}}, Target: parser.RoleSpec{Name: "ownership_target"}}
	if _, err := testExecute(e, context.Background(), late, nil); err == nil || e.catalog.state != before || e.roles != roles {
		t.Fatal("late-source preflight partially published", err)
	}
	late.Sources = late.Sources[:1]
	if _, err := testExecute(e, context.Background(), late, nil); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state.MustRelation(move.Address()).Owner != target || e.catalog.state.Get(other.Address()) != before.Get(other.Address()) || e.roles != roles || e.storage.relations[move.Address()] != store {
		t.Fatal("reassign changed unrelated grant, roles or data")
	}
}

func TestDropOwnedTypeACLAndCancellation(t *testing.T) {
	e, owner, source, _, reader := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TYPE ownership_cleanup_e AS ENUM('a')")
	base := e.CatalogRead()
	address := base.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_cleanup_e"})
	typ := base.MustType(address)
	setup := e.BeginCatalogStage(base)
	for _, a := range []ids.ObjectAddress{address, {ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}} {
		old := base.MustType(a)
		next := *old
		next.ObjectRevision++
		next.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.UsagePrivilege, authorization.UsagePrivilege), objectACLUnitEntry(source, reader, authorization.UsagePrivilege, 0)}}
		next.ACLSyntheticDefault = false
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	roles := e.roles
	stage := e.BeginCatalogStage(base)
	stage.roleRevision = roles.revision
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := stageOwnedCleanup(ctx, stage, e.roleRead(base, roles), roles, map[authorization.PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(stage.Descriptors) != 0 || e.catalog.state != base.State() || e.roles != roles {
		t.Fatal("canceled cleanup mutated state")
	}
	stage = e.BeginCatalogStage(base)
	stage.roleRevision = roles.revision
	if err := stageOwnedCleanup(context.Background(), stage, e.roleRead(base, roles), roles, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	for _, a := range []ids.ObjectAddress{address, {ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}} {
		old := base.MustType(a)
		next := e.catalog.state.MustType(a)
		if next.Owner != owner || next.SQLType != old.SQLType || next.Array != old.Array || next.OwningRelation != old.OwningRelation || len(next.ACL.Entries) != 0 || len(old.ACL.Entries) != 2 {
			t.Fatal("type ACL cleanup broke owner/reciprocity/retained signature", a)
		}
	}
}

func TestDropOwnedCollectedCleanupPrimary(t *testing.T) {
	for _, tc := range []struct {
		name, policyTable, policyRoles    string
		membership, acl, sequence, plural bool
	}{
		{name: "sole owner root"},
		{name: "real column-owned sequence", sequence: true, plural: true},
		{name: "grantor membership deletion", membership: true, plural: true},
		{name: "sole policy on owner root", policyTable: "ownership_primary", policyRoles: "ownership_source", plural: true},
		{name: "sole policy on external table", policyTable: "ownership_external", policyRoles: "ownership_source", plural: true},
		{name: "partial external policy survives", policyTable: "ownership_external", policyRoles: "ownership_source,ownership_reader"},
		{name: "ACL edit is not a deletion root", acl: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, owner, source, target, reader := ownershipUnitRoles()
			column := "id INT"
			if tc.sequence {
				column = "id SERIAL"
			}
			for _, q := range []string{
				"CREATE TABLE ownership_primary(" + column + ")",
				"INSERT INTO ownership_primary VALUES(7)",
				"CREATE TABLE ownership_external(id INT)",
				"CREATE VIEW ownership_consumer AS SELECT id FROM ownership_primary",
				"ALTER TABLE ownership_primary OWNER TO ownership_source",
			} {
				ownershipUnitExec(t, e, q)
			}
			if tc.policyTable != "" {
				ownershipUnitExec(t, e, "CREATE POLICY ownership_cleanup_policy ON "+tc.policyTable+" TO "+tc.policyRoles+" USING (true)")
			}
			if tc.acl {
				ownershipUnitExec(t, e, "GRANT SELECT ON ownership_external TO ownership_source")
			}
			if tc.membership {
				graph := cloneRoleFixture(e.roles)
				setFixtureEdges(graph, append(fixtureEdges(graph),
					authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true, Inherit: true, Set: true},
					authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true}))
				graph.revision++
				e.roles = graph
			}
			before, roles := e.catalog.state, e.roles
			address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_primary"})
			store := e.storage.relations[address]
			versions := store.versions
			_, err := testExecute(e, context.Background(), &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}, nil)
			var diagnostic *sqlstate.Error
			want := "cannot drop table ownership_primary because other objects depend on it"
			if tc.plural {
				want = "cannot drop desired object(s) because other objects depend on them"
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != want {
				t.Fatal("collected deletion primary", err, "want", want)
			}
			if e.catalog.state != before || e.roles != roles || e.storage.relations[address] != store || &store.versions[0] != &versions[0] {
				t.Fatal("private cleanup or dependency refusal partially published")
			}
		})
	}
}

func TestDropOwnedCleanupPolicyDeletionUnion(t *testing.T) {
	e, _, _, _, _ := ownershipUnitRoles()
	for _, q := range []string{
		"CREATE TABLE ownership_policy_root(id INT)",
		"CREATE TABLE ownership_policy_survivor(id INT)",
		"INSERT INTO ownership_policy_survivor VALUES(7)",
		"ALTER TABLE ownership_policy_root OWNER TO ownership_source",
		"CREATE POLICY ownership_policy_doomed ON ownership_policy_survivor TO ownership_source USING ('ownership_policy_root'::regclass IS NOT NULL)",
	} {
		ownershipUnitExec(t, e, q)
	}
	before := e.catalog.state
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	root := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_root"})
	survivor := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_survivor"})
	original := before.MustRelation(survivor)
	store := e.storage.relations[survivor]
	versions := store.versions
	ownershipUnitExec(t, e, "DROP OWNED BY ownership_source RESTRICT")
	if e.catalog.state.Get(root) != nil || len(e.catalog.state.MustRelation(survivor).Security.Policies) != 0 {
		t.Fatal("pending sole-target policy falsely restricted its owner-root dependency")
	}
	if len(original.Security.Policies) != 1 || before.Get(root) == nil || e.storage.relations[survivor] != store || &store.versions[0] != &versions[0] {
		t.Fatal("cleanup changed retained policy/catalog or surviving rows")
	}
}

func TestDropOwnedACLGrantorSupport(t *testing.T) {
	const both = authorization.SelectPrivilege | authorization.UpdatePrivilege
	for _, tc := range []struct {
		name                               string
		inheritedOwner, independentOptions bool
		support                            authorization.Privileges
	}{
		{name: "inherited owner", inheritedOwner: true, support: both},
		{name: "SET-only owner"},
		{name: "independent inherited options", independentOptions: true, support: authorization.SelectPrivilege},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, owner, source, target, reader := ownershipUnitRoles()
			graph := cloneRoleFixture(e.roles)
			setFixtureEdges(graph, []authorization.Membership{{Role: owner, Member: source, Grantor: owner, Inherit: tc.inheritedOwner, Set: true}})
			old := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				objectACLUnitEntry(source, reader, both, both),
				objectACLUnitEntry(reader, target, both, 0),
				objectACLUnitEntry(owner, source, both, both),
			}}
			if tc.independentOptions {
				setFixtureEdges(graph, append(fixtureEdges(graph), authorization.Membership{Role: target, Member: source, Grantor: owner, Inherit: true, Set: false}))
				old.Entries = append(old.Entries, objectACLUnitEntry(owner, target, authorization.SelectPrivilege, authorization.SelectPrivilege))
			}
			retained := append([]authorization.ACLEntry(nil), old.Entries...)
			edges := append([]authorization.Membership(nil), fixtureEdges(graph)...)
			catalog := e.CatalogRead()
			roles := e.roleRead(catalog, graph)
			got, changed, err := authorization.OwnedACL(context.Background(), roles, old, owner, map[authorization.PrincipalRef]bool{source: true})
			expected := authorization.ACLValue{Explicit: true}
			if tc.support != 0 {
				expected.Entries = []authorization.ACLEntry{
					objectACLUnitEntry(source, reader, tc.support, tc.support),
					objectACLUnitEntry(reader, target, tc.support, 0),
				}
			}
			if tc.independentOptions {
				expected.Entries = append(expected.Entries, objectACLUnitEntry(owner, target, authorization.SelectPrivilege, authorization.SelectPrivilege))
			}
			if err != nil || !changed || got.Explicit != expected.Explicit || !slices.Equal(got.Entries, expected.Entries) {
				t.Fatal("received-first selective grantor support", got, err, "want", expected)
			}
			if !reflect.DeepEqual(old.Entries, retained) || !reflect.DeepEqual(fixtureEdges(graph), edges) {
				t.Fatal("cleanup mutated captured ACL or membership graph")
			}
			if tc.inheritedOwner {
				withoutOwner := cloneRoleFixture(graph)
				setFixtureEdges(withoutOwner, nil)
				regranted := authorization.ACLValue{Explicit: true, Entries: append(append([]authorization.ACLEntry(nil), got.Entries...), objectACLUnitEntry(owner, source, both, both))}
				retainedSupported := append([]authorization.ACLEntry(nil), got.Entries...)
				lost, different, err := authorization.OwnedACL(context.Background(), e.roleRead(catalog, withoutOwner), regranted, owner, map[authorization.PrincipalRef]bool{source: true})
				if err != nil || !different || len(lost.Entries) != 0 || !reflect.DeepEqual(got.Entries, retainedSupported) {
					t.Fatal("unsupported provenance after membership loss", lost, err)
				}
			}
		})
	}
}

func TestDropOwnedIndependentSequenceCollectedOnce(t *testing.T) {
	e, _, _, _, _ := ownershipUnitRoles()
	for _, q := range []string{
		"CREATE SEQUENCE ownership_direct_sequence",
		"ALTER SEQUENCE ownership_direct_sequence OWNER TO ownership_source",
		"CREATE VIEW ownership_sequence_consumer AS SELECT nextval('ownership_direct_sequence'::regclass) AS n",
	} {
		ownershipUnitExec(t, e, q)
	}
	before, roles := e.catalog.state, e.roles
	_, err := testExecute(e, context.Background(), &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop sequence ownership_direct_sequence because other objects depend on it" {
		t.Fatal("independent sequence was counted twice", err)
	}
	if e.catalog.state != before || e.roles != roles {
		t.Fatal("refusal changed retained roots")
	}
}

func TestDropOwnedSQLGrantorProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE ROLE ownership_sql_owner",
		"CREATE ROLE ownership_sql_source",
		"CREATE ROLE ownership_sql_downstream",
		"CREATE TABLE ownership_sql_support(id INT)",
		"INSERT INTO ownership_sql_support VALUES(7)",
		"ALTER TABLE ownership_sql_support OWNER TO ownership_sql_owner",
		"GRANT ownership_sql_owner TO ownership_sql_source WITH INHERIT TRUE, SET FALSE",
		"SET ROLE ownership_sql_owner",
		"GRANT SELECT ON ownership_sql_support TO ownership_sql_source WITH GRANT OPTION",
		"RESET ROLE",
		"SET ROLE ownership_sql_source",
		"GRANT SELECT ON ownership_sql_support TO ownership_sql_downstream",
		"RESET ROLE",
	} {
		roleExecute(t, e, op, q)
	}
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_owner")
	source := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_source")
	downstream := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_downstream")
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_sql_support"})
	original := e.catalog.state.MustRelation(address)
	received := objectACLUnitEntry(owner, source, authorization.SelectPrivilege, authorization.SelectPrivilege)
	delegated := objectACLUnitEntry(source, downstream, authorization.SelectPrivilege, 0)
	if original.Owner != owner || !slices.Contains(original.Security.ACL.Entries, received) || !slices.Contains(original.Security.ACL.Entries, delegated) {
		t.Fatal("actual SQL GRANT lost current-role provenance", original.Security.ACL)
	}
	retained := append([]authorization.ACLEntry(nil), original.Security.ACL.Entries...)
	store := e.storage.relations[address]
	roleExecute(t, e, op, "DROP OWNED BY ownership_sql_source RESTRICT")
	current := e.catalog.state.MustRelation(address)
	if current.Owner != owner || slices.Contains(current.Security.ACL.Entries, received) || !slices.Contains(current.Security.ACL.Entries, delegated) {
		t.Fatal("cleanup did not retain supported SQL-origin delegation", current.Security.ACL)
	}
	if !slices.Equal(original.Security.ACL.Entries, retained) || e.storage.relations[address] != store {
		t.Fatal("cleanup changed retained ACL or surviving storage")
	}
	before, roles := e.catalog.state, e.roles
	roleError(t, e, op, "DROP ROLE ownership_sql_source", "2BP01", `role "ownership_sql_source" cannot be dropped because some objects depend on it`, "privileges for table ownership_sql_support")
	if e.catalog.state != before || e.roles != roles {
		t.Fatal("retained grantor blocker partially retired the source")
	}
}
