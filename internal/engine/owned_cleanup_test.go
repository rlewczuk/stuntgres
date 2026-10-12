package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDropOwnedACLSelectiveCascade(t *testing.T) {
	for _, mask := range []Privileges{SelectPrivilege, UsagePrivilege, CreatePrivilege} {
		for _, alternate := range []bool{false, true} {
			e, owner, source, target, reader := ownershipUnitRoles()
			entries := []ACLEntry{grantUnitEntry(owner, source, mask, mask), grantUnitEntry(owner, target, mask, mask), grantUnitEntry(source, reader, mask, mask), grantUnitEntry(reader, target, mask, 0)}
			if alternate {
				entries = append(entries, grantUnitEntry(target, reader, mask, mask))
			}
			old := ACLValue{Explicit: true, Entries: entries}
			retained := append([]ACLEntry(nil), entries...)
			got, changed, err := ownedACL(context.Background(), RoleRead{e, e.roles, CatalogRead{e, e.state}}, old, owner, map[PrincipalRef]bool{source: true})
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
			if _, _, err := ownedACL(ctx, RoleRead{e, e.roles, CatalogRead{e, e.state}}, old, owner, map[PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
func TestDropOwnedPolicyTargets(t *testing.T) {
	_, _, source, target, _ := ownershipUnitRoles()
	old := []policyDefinition{{Name: "mixed", roles: []policyRole{{ref: source}, {ref: target}, {ref: source}}}, {Name: "last", roles: []policyRole{{ref: source}, {ref: source}}}, {Name: "public", roles: []policyRole{{public: true}, {ref: source}}}}
	before := []int{len(old[0].roles), len(old[1].roles), len(old[2].roles)}
	got, changed := ownedPolicies(old, map[PrincipalRef]bool{source: true})
	if !changed || len(got) != 2 || got[0].Name != "mixed" || len(got[0].roles) != 1 || got[0].roles[0].ref != target || got[1].Name != "public" || len(got[1].roles) != 1 || !got[1].roles[0].public {
		t.Fatal(got, changed)
	}
	for i, n := range before {
		if len(old[i].roles) != n {
			t.Fatal("policy input mutated", old)
		}
	}
}
func TestDropOwnedMembershipProvenance(t *testing.T) {
	e, owner, source, target, reader := ownershipUnitRoles()
	e.roles.edges = append(e.roles.edges, Membership{Role: target, Member: source, Grantor: owner, Admin: true}, Membership{Role: target, Member: reader, Grantor: source, Admin: true, Inherit: true, Set: true}, Membership{Role: target, Member: owner, Grantor: reader, Inherit: true})
	old := e.roles
	retained := append([]Membership(nil), old.edges...)
	next, err := ownedMembershipCandidate(context.Background(), RoleRead{e, old, CatalogRead{e, e.state}}, map[PrincipalRef]bool{source: true})
	if err != nil || next == nil || next.revision != old.revision+1 || !reflect.DeepEqual(old.edges, retained) {
		t.Fatal(next, err)
	}
	for _, edge := range next.edges {
		if edge.Grantor == source || edge.Grantor == reader {
			t.Fatal("unsupported downstream membership survived", edge)
		}
	}
	if len(next.roles) != len(old.roles) || next.roles[source] != old.roles[source] {
		t.Fatal("DROP OWNED removed role endpoint")
	}
	if noChange, err := ownedMembershipCandidate(context.Background(), RoleRead{e, old, CatalogRead{e, e.state}}, map[PrincipalRef]bool{target: true}); err != nil || noChange != nil {
		t.Fatal("unchanged membership cloned/revised", noChange, err)
	}
}
func TestDropOwnedUnionRestrictAndCascade(t *testing.T) {
	e, _, source, _, _ := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_parent(id INT)", "CREATE TABLE ownership_child(id INT)", "INSERT INTO ownership_parent VALUES(7)", "CREATE VIEW ownership_external AS SELECT id FROM ownership_parent"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	setup := e.BeginCatalogStage(base)
	for _, name := range []string{"ownership_parent", "ownership_child"} {
		old := base.state.descriptors[base.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: name}]].(*relationDescriptor)
		next := *old
		next.owner = source
		next.revision++
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	oldState, oldRoles := e.state, e.roles
	parent := oldState.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: "ownership_parent"}]
	store := e.storage[parent]
	rows := store.versions
	stmt := &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}
	_, err := testExecute(e, context.Background(), stmt, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop desired object(s) because other objects depend on them" {
		t.Fatal("actual external refusal", err)
	}
	if e.state != oldState || e.roles != oldRoles || e.storage[parent] != store || &store.versions[0] != &rows[0] {
		t.Fatal("RESTRICT partially published")
	}
	stmt.Mode = parser.DropCascade
	if _, err = testExecute(e, context.Background(), stmt, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ownership_parent", "ownership_child", "ownership_external"} {
		if _, exists := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: name}]; exists {
			t.Fatal("actual cascade retained object", name)
		}
	}
	if _, exists := e.roles.roles[source]; !exists || oldState.descriptors[parent] == nil || len(rows) != 1 {
		t.Fatal("role/retained snapshot lost")
	}
}
func TestReassignOwnedSourceSetAndUnrelatedGrant(t *testing.T) {
	e, owner, source, target, reader := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_move(id INT)", "CREATE TABLE ownership_other(id INT)", "INSERT INTO ownership_move VALUES(7)"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	setup := e.BeginCatalogStage(base)
	move := base.state.descriptors[base.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: "ownership_move"}]].(*relationDescriptor)
	next := *move
	next.owner = source
	next.revision++
	if err := setup.Replace(&next); err != nil {
		t.Fatal(err)
	}
	other := base.state.descriptors[base.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: "ownership_other"}]].(*relationDescriptor)
	grant := *other
	grant.acl = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, source, SelectPrivilege, SelectPrivilege), grantUnitEntry(source, reader, SelectPrivilege, 0)}}
	grant.revision++
	if err := setup.Replace(&grant); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, setup)
	before := e.state
	store := e.storage[move.address]
	roles := e.roles
	late := &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}, {Name: "absent"}}, Target: parser.RoleSpec{Name: "ownership_target"}}
	if _, err := testExecute(e, context.Background(), late, nil); err == nil || e.state != before || e.roles != roles {
		t.Fatal("late-source preflight partially published", err)
	}
	late.Sources = late.Sources[:1]
	if _, err := testExecute(e, context.Background(), late, nil); err != nil {
		t.Fatal(err)
	}
	if e.state.descriptors[move.address].(*relationDescriptor).owner != target || e.state.descriptors[other.address] != before.descriptors[other.address] || e.roles != roles || e.storage[move.address] != store {
		t.Fatal("reassign changed unrelated grant, roles or data")
	}
}

func TestDropOwnedTypeACLAndCancellation(t *testing.T) {
	e, owner, source, _, reader := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TYPE ownership_cleanup_e AS ENUM('a')")
	base := e.CatalogRead()
	address := base.state.names[NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "ownership_cleanup_e"}]
	typ := base.state.descriptors[address].(*typeDescriptor)
	setup := e.BeginCatalogStage(base)
	for _, a := range []ObjectAddress{address, {ClassID: TypeClassID, ObjectID: OID(typ.array)}} {
		old := base.state.descriptors[a].(*typeDescriptor)
		next := *old
		next.revision++
		next.acl = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, source, UsagePrivilege, UsagePrivilege), grantUnitEntry(source, reader, UsagePrivilege, 0)}}
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
	if err := stageOwnedCleanup(ctx, stage, RoleRead{e, roles, base}, map[PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(stage.descriptors) != 0 || e.state != base.state || e.roles != roles {
		t.Fatal("canceled cleanup mutated state")
	}
	stage = e.BeginCatalogStage(base)
	stage.roleRevision = roles.revision
	if err := stageOwnedCleanup(context.Background(), stage, RoleRead{e, roles, base}, map[PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	for _, a := range []ObjectAddress{address, {ClassID: TypeClassID, ObjectID: OID(typ.array)}} {
		old := base.state.descriptors[a].(*typeDescriptor)
		next := e.state.descriptors[a].(*typeDescriptor)
		if next.owner != owner || next.typ != old.typ || next.array != old.array || next.owningRelation != old.owningRelation || len(next.acl.Entries) != 0 || len(old.acl.Entries) != 2 {
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
				graph := cloneRoles(e.roles)
				graph.edges = append(graph.edges,
					Membership{Role: target, Member: source, Grantor: owner, Admin: true, Inherit: true, Set: true},
					Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true})
				graph.revision++
				e.roles = graph
			}
			before, roles := e.state, e.roles
			address := before.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "ownership_primary"}]
			store := e.storage[address]
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
			if e.state != before || e.roles != roles || e.storage[address] != store || &store.versions[0] != &versions[0] {
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
	before := e.state
	namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	root := before.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "ownership_policy_root"}]
	survivor := before.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "ownership_policy_survivor"}]
	original := before.descriptors[survivor].(*relationDescriptor)
	store := e.storage[survivor]
	versions := store.versions
	ownershipUnitExec(t, e, "DROP OWNED BY ownership_source RESTRICT")
	if e.state.descriptors[root] != nil || len(e.state.descriptors[survivor].(*relationDescriptor).policies) != 0 {
		t.Fatal("pending sole-target policy falsely restricted its owner-root dependency")
	}
	if len(original.policies) != 1 || before.descriptors[root] == nil || e.storage[survivor] != store || &store.versions[0] != &versions[0] {
		t.Fatal("cleanup changed retained policy/catalog or surviving rows")
	}
}

func TestDropOwnedACLGrantorSupport(t *testing.T) {
	const both = SelectPrivilege | UpdatePrivilege
	for _, tc := range []struct {
		name                               string
		inheritedOwner, independentOptions bool
		support                            Privileges
	}{
		{name: "inherited owner", inheritedOwner: true, support: both},
		{name: "SET-only owner"},
		{name: "independent inherited options", independentOptions: true, support: SelectPrivilege},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, owner, source, target, reader := ownershipUnitRoles()
			graph := cloneRoles(e.roles)
			graph.edges = []Membership{{Role: owner, Member: source, Grantor: owner, Inherit: tc.inheritedOwner, Set: true}}
			old := ACLValue{Explicit: true, Entries: []ACLEntry{
				grantUnitEntry(source, reader, both, both),
				grantUnitEntry(reader, target, both, 0),
				grantUnitEntry(owner, source, both, both),
			}}
			if tc.independentOptions {
				graph.edges = append(graph.edges, Membership{Role: target, Member: source, Grantor: owner, Inherit: true, Set: false})
				old.Entries = append(old.Entries, grantUnitEntry(owner, target, SelectPrivilege, SelectPrivilege))
			}
			retained := append([]ACLEntry(nil), old.Entries...)
			edges := append([]Membership(nil), graph.edges...)
			roles := RoleRead{e, graph, e.CatalogRead()}
			got, changed, err := ownedACL(context.Background(), roles, old, owner, map[PrincipalRef]bool{source: true})
			expected := ACLValue{Explicit: true}
			if tc.support != 0 {
				expected.Entries = []ACLEntry{
					grantUnitEntry(source, reader, tc.support, tc.support),
					grantUnitEntry(reader, target, tc.support, 0),
				}
			}
			if tc.independentOptions {
				expected.Entries = append(expected.Entries, grantUnitEntry(owner, target, SelectPrivilege, SelectPrivilege))
			}
			if err != nil || !changed || got.Explicit != expected.Explicit || !slices.Equal(got.Entries, expected.Entries) {
				t.Fatal("received-first selective grantor support", got, err, "want", expected)
			}
			if !reflect.DeepEqual(old.Entries, retained) || !reflect.DeepEqual(graph.edges, edges) {
				t.Fatal("cleanup mutated captured ACL or membership graph")
			}
			if tc.inheritedOwner {
				withoutOwner := cloneRoles(graph)
				withoutOwner.edges = nil
				regranted := ACLValue{Explicit: true, Entries: append(append([]ACLEntry(nil), got.Entries...), grantUnitEntry(owner, source, both, both))}
				retainedSupported := append([]ACLEntry(nil), got.Entries...)
				lost, different, err := ownedACL(context.Background(), RoleRead{e, withoutOwner, roles.catalog}, regranted, owner, map[PrincipalRef]bool{source: true})
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
	before, roles := e.state, e.roles
	_, err := testExecute(e, context.Background(), &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop sequence ownership_direct_sequence because other objects depend on it" {
		t.Fatal("independent sequence was counted twice", err)
	}
	if e.state != before || e.roles != roles {
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
	owner := e.roles.names["ownership_sql_owner"]
	source := e.roles.names["ownership_sql_source"]
	downstream := e.roles.names["ownership_sql_downstream"]
	address := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "ownership_sql_support"}]
	original := e.state.descriptors[address].(*relationDescriptor)
	received := grantUnitEntry(owner, source, SelectPrivilege, SelectPrivilege)
	delegated := grantUnitEntry(source, downstream, SelectPrivilege, 0)
	if original.owner != owner || !slices.Contains(original.acl.Entries, received) || !slices.Contains(original.acl.Entries, delegated) {
		t.Fatal("actual SQL GRANT lost current-role provenance", original.acl)
	}
	retained := append([]ACLEntry(nil), original.acl.Entries...)
	store := e.storage[address]
	roleExecute(t, e, op, "DROP OWNED BY ownership_sql_source RESTRICT")
	current := e.state.descriptors[address].(*relationDescriptor)
	if current.owner != owner || slices.Contains(current.acl.Entries, received) || !slices.Contains(current.acl.Entries, delegated) {
		t.Fatal("cleanup did not retain supported SQL-origin delegation", current.acl)
	}
	if !slices.Equal(original.acl.Entries, retained) || e.storage[address] != store {
		t.Fatal("cleanup changed retained ACL or surviving storage")
	}
	before, roles := e.state, e.roles
	roleError(t, e, op, "DROP ROLE ownership_sql_source", "2BP01", `role "ownership_sql_source" cannot be dropped because some objects depend on it`, "privileges for table ownership_sql_support")
	if e.state != before || e.roles != roles {
		t.Fatal("retained grantor blocker partially retired the source")
	}
}
