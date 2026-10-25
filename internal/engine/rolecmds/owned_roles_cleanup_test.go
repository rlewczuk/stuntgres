package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
			got, changed, err := authorization.OwnedACL(context.Background(), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), old, owner, map[authorization.PrincipalRef]bool{source: true})
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
			if _, _, err := authorization.OwnedACL(ctx, e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), old, owner, map[authorization.PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
func TestDropOwnedMembershipProvenance(t *testing.T) {
	e, owner, source, target, reader := ownershipUnitRoles()
	setFixtureEdges(e.RoleStore.Published, append(fixtureEdges(e.RoleStore.Published), authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true}, authorization.Membership{Role: target, Member: reader, Grantor: source, Admin: true, Inherit: true, Set: true}, authorization.Membership{Role: target, Member: owner, Grantor: reader, Inherit: true}))
	old := e.RoleStore.Published
	retained := append([]authorization.Membership(nil), fixtureEdges(old)...)
	next, err := access.OwnedMembershipCandidate(context.Background(), e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), old), old.Revision, map[authorization.PrincipalRef]bool{source: true})
	if err != nil || next == nil || next.Revision != old.Revision+1 || !reflect.DeepEqual(fixtureEdges(old), retained) {
		t.Fatal(next, err)
	}
	for _, edge := range fixtureEdges(next) {
		if edge.Grantor == source || edge.Grantor == reader {
			t.Fatal("unsupported downstream membership survived", edge)
		}
	}
	var want []authorization.Membership
	for _, edge := range retained {
		if edge.Grantor != source && edge.Grantor != reader {
			want = append(want, edge)
		}
	}
	if !reflect.DeepEqual(fixtureEdges(next), want) {
		t.Fatal("surviving membership identity/flags/order changed", fixtureEdges(next), want)
	}
	// An independent ADMIN origin supports the reader's downstream grant even
	// after the source-origin edge disappears. Every surviving flag is exact.
	alternate := cloneRoleFixture(old)
	setFixtureEdges(alternate, append(append([]authorization.Membership(nil), retained...), authorization.Membership{Role: target, Member: reader, Grantor: owner, Admin: true, Set: true}))
	alternateBefore := append([]authorization.Membership(nil), fixtureEdges(alternate)...)
	supported, err := access.OwnedMembershipCandidate(context.Background(), e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), alternate), alternate.Revision, map[authorization.PrincipalRef]bool{source: true})
	if err != nil || supported == nil {
		t.Fatal("independent membership support", err)
	}
	want = nil
	for _, edge := range alternateBefore {
		if edge.Grantor != source {
			want = append(want, edge)
		}
	}
	if !reflect.DeepEqual(fixtureEdges(supported), want) || !reflect.DeepEqual(fixtureEdges(alternate), alternateBefore) {
		t.Fatal("alternate support/provenance or source root changed", fixtureEdges(supported), want)
	}
	if next.Root.Read(authorization.PrincipalRef{}).RoleCount() != old.Root.Read(authorization.PrincipalRef{}).RoleCount() || fixtureRole(next, source) != fixtureRole(old, source) {
		t.Fatal("DROP OWNED removed role endpoint")
	}
	if noChange, err := access.OwnedMembershipCandidate(context.Background(), e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), old), old.Revision, map[authorization.PrincipalRef]bool{target: true}); err != nil || noChange != nil {
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
	oldState, oldRoles := e.Catalog.State, e.RoleStore.Published
	parent := oldState.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ownership_parent"})
	store := e.Storage.Relations[parent]
	rows := store.Versions
	stmt := &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}
	_, err := enginetest.Execute(e, context.Background(), stmt, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop desired object(s) because other objects depend on them" {
		t.Fatal("actual external refusal", err)
	}
	if e.Catalog.State != oldState || e.RoleStore.Published != oldRoles || e.Storage.Relations[parent] != store || &store.Versions[0] != &rows[0] {
		t.Fatal("RESTRICT partially published")
	}
	stmt.Mode = parser.DropCascade
	if _, err = enginetest.Execute(e, context.Background(), stmt, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ownership_parent", "ownership_child", "ownership_external"} {
		if _, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}); exists {
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
	before := e.Catalog.State
	store := e.Storage.Relations[move.Address()]
	roles := e.RoleStore.Published
	late := &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}, {Name: "absent"}}, Target: parser.RoleSpec{Name: "ownership_target"}}
	if _, err := enginetest.Execute(e, context.Background(), late, nil); err == nil || e.Catalog.State != before || e.RoleStore.Published != roles {
		t.Fatal("late-source preflight partially published", err)
	}
	late.Sources = late.Sources[:1]
	if _, err := enginetest.Execute(e, context.Background(), late, nil); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.State.MustRelation(move.Address()).Owner != target || e.Catalog.State.Get(other.Address()) != before.Get(other.Address()) || e.RoleStore.Published != roles || e.Storage.Relations[move.Address()] != store {
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
	roles := e.RoleStore.Published
	stage := e.BeginCatalogStage(base)
	stage.RoleRevision = roles.Revision
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := role.StageOwnedCleanup(ctx, stage, e.RoleStore.Read(&e.Catalog.Owner, base, roles), roles, map[authorization.PrincipalRef]bool{source: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stage.DescriptorCount() != 0 || e.Catalog.State != base.State() || e.RoleStore.Published != roles {
		t.Fatal("canceled cleanup mutated state")
	}
	stage = e.BeginCatalogStage(base)
	stage.RoleRevision = roles.Revision
	if err := role.StageOwnedCleanup(context.Background(), stage, e.RoleStore.Read(&e.Catalog.Owner, base, roles), roles, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	for _, a := range []ids.ObjectAddress{address, {ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}} {
		old := base.MustType(a)
		next := e.Catalog.State.MustType(a)
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
				graph := cloneRoleFixture(e.RoleStore.Published)
				setFixtureEdges(graph, append(fixtureEdges(graph),
					authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true, Inherit: true, Set: true},
					authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true}))
				graph.Revision++
				e.RoleStore.PublishLocked(graph)
			}
			before, roles := e.Catalog.State, e.RoleStore.Published
			address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_primary"})
			store := e.Storage.Relations[address]
			versions := store.Versions
			_, err := enginetest.Execute(e, context.Background(), &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}, nil)
			var diagnostic *sqlstate.Error
			want := "cannot drop table ownership_primary because other objects depend on it"
			if tc.plural {
				want = "cannot drop desired object(s) because other objects depend on them"
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != want {
				t.Fatal("collected deletion primary", err, "want", want)
			}
			if e.Catalog.State != before || e.RoleStore.Published != roles || e.Storage.Relations[address] != store || &store.Versions[0] != &versions[0] {
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
	before := e.Catalog.State
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	root := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_root"})
	survivor := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ownership_policy_survivor"})
	original := before.MustRelation(survivor)
	store := e.Storage.Relations[survivor]
	versions := store.Versions
	ownershipUnitExec(t, e, "DROP OWNED BY ownership_source RESTRICT")
	if e.Catalog.State.Get(root) != nil || len(e.Catalog.State.MustRelation(survivor).Security.Policies) != 0 {
		t.Fatal("pending sole-target policy falsely restricted its owner-root dependency")
	}
	if len(original.Security.Policies) != 1 || before.Get(root) == nil || e.Storage.Relations[survivor] != store || &store.Versions[0] != &versions[0] {
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
			graph := cloneRoleFixture(e.RoleStore.Published)
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
			roles := e.RoleStore.Read(&e.Catalog.Owner, catalog, graph)
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
				lost, different, err := authorization.OwnedACL(context.Background(), e.RoleStore.Read(&e.Catalog.Owner, catalog, withoutOwner), regranted, owner, map[authorization.PrincipalRef]bool{source: true})
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
	before, roles := e.Catalog.State, e.RoleStore.Published
	_, err := enginetest.Execute(e, context.Background(), &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "ownership_source"}}, Mode: parser.DropRestrict}, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop sequence ownership_direct_sequence because other objects depend on it" {
		t.Fatal("independent sequence was counted twice", err)
	}
	if e.Catalog.State != before || e.RoleStore.Published != roles {
		t.Fatal("refusal changed retained roots")
	}
}

func TestDropOwnedSQLGrantorProvenance(t *testing.T) {
	e := engine.New()
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
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_owner")
	source := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_source")
	downstream := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_sql_downstream")
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_sql_support"})
	original := e.Catalog.State.MustRelation(address)
	received := objectACLUnitEntry(owner, source, authorization.SelectPrivilege, authorization.SelectPrivilege)
	delegated := objectACLUnitEntry(source, downstream, authorization.SelectPrivilege, 0)
	if original.Owner != owner || !slices.Contains(original.Security.ACL.Entries, received) || !slices.Contains(original.Security.ACL.Entries, delegated) {
		t.Fatal("actual SQL GRANT lost current-role provenance", original.Security.ACL)
	}
	retained := append([]authorization.ACLEntry(nil), original.Security.ACL.Entries...)
	store := e.Storage.Relations[address]
	roleExecute(t, e, op, "DROP OWNED BY ownership_sql_source RESTRICT")
	current := e.Catalog.State.MustRelation(address)
	if current.Owner != owner || slices.Contains(current.Security.ACL.Entries, received) || !slices.Contains(current.Security.ACL.Entries, delegated) {
		t.Fatal("cleanup did not retain supported SQL-origin delegation", current.Security.ACL)
	}
	if !slices.Equal(original.Security.ACL.Entries, retained) || e.Storage.Relations[address] != store {
		t.Fatal("cleanup changed retained ACL or surviving storage")
	}
	before, roles := e.Catalog.State, e.RoleStore.Published
	roleError(t, e, op, "DROP ROLE ownership_sql_source", "2BP01", `role "ownership_sql_source" cannot be dropped because some objects depend on it`, "privileges for table ownership_sql_support")
	if e.Catalog.State != before || e.RoleStore.Published != roles {
		t.Fatal("retained grantor blocker partially retired the source")
	}
}

func TestDropOwnedPolicyRelationLocks(t *testing.T) {
	e, _, source, target, reader := ownershipUnitRoles()
	for _, q := range []string{
		"CREATE TABLE ownership_lock_root(id INT)",
		"CREATE INDEX ownership_lock_root_i ON ownership_lock_root(id)",
		"CREATE SEQUENCE ownership_lock_root_s OWNED BY ownership_lock_root.id",
		"ALTER TABLE ownership_lock_root OWNER TO ownership_source",
		"CREATE TABLE ownership_lock_survivor(id INT)",
		"CREATE INDEX ownership_lock_survivor_i ON ownership_lock_survivor(id)",
		"CREATE SEQUENCE ownership_lock_survivor_s OWNED BY ownership_lock_survivor.id",
		"CREATE POLICY lock_last_source ON ownership_lock_survivor TO ownership_source,ownership_source USING(true)",
		"CREATE POLICY lock_last_reader ON ownership_lock_survivor TO ownership_reader USING(true)",
		"CREATE TABLE ownership_lock_union(id INT)",
		"CREATE POLICY lock_union ON ownership_lock_union TO ownership_source,ownership_reader USING(true)",
		"CREATE TABLE ownership_lock_partial(id INT)",
		"CREATE POLICY lock_partial ON ownership_lock_partial TO ownership_source,ownership_target,ownership_source USING(true)",
		"CREATE TABLE ownership_lock_public(id INT)",
		"CREATE POLICY lock_public ON ownership_lock_public TO PUBLIC USING(false)",
		"CREATE TABLE ownership_lock_acl(id INT)",
		"GRANT SELECT ON ownership_lock_acl TO ownership_source",
	} {
		ownershipUnitExec(t, e, q)
	}
	view := e.CatalogRead()
	base := e.Catalog.State
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := func(name string) ids.ObjectAddress {
		a := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
		if a == (ids.ObjectAddress{}) {
			t.Fatal("missing actual relation fixture", name)
		}
		return a
	}
	root, index, sequence := address("ownership_lock_root"), address("ownership_lock_root_i"), address("ownership_lock_root_s")
	survivor, union, partial := address("ownership_lock_survivor"), address("ownership_lock_union"), address("ownership_lock_partial")
	original := view.MustRelation(survivor)
	retained := slices.Clone(original.Security.Policies)
	for i := range retained {
		retained[i].Roles = slices.Clone(retained[i].Roles)
	}
	for _, tc := range []struct {
		name    string
		roots   []ids.ObjectAddress
		sources map[authorization.PrincipalRef]bool
		want    []ids.ObjectAddress
	}{
		{"non-cleanup", []ids.ObjectAddress{root}, nil, []ids.ObjectAddress{root, index, sequence}},
		{"source-and-root-dedup", []ids.ObjectAddress{root, root}, map[authorization.PrincipalRef]bool{source: true}, []ids.ObjectAddress{root, index, sequence, survivor}},
		{"reader-zero-owned-roots", nil, map[authorization.PrincipalRef]bool{reader: true}, []ids.ObjectAddress{survivor}},
		{"complete-source-reader-union", []ids.ObjectAddress{root}, map[authorization.PrincipalRef]bool{source: true, reader: true}, []ids.ObjectAddress{root, index, sequence, survivor, union}},
		{"complete-source-target-union", []ids.ObjectAddress{root}, map[authorization.PrincipalRef]bool{source: true, target: true}, []ids.ObjectAddress{root, index, sequence, survivor, partial}},
		{"mixed-public-acl-only", nil, map[authorization.PrincipalRef]bool{target: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sort.Slice(tc.want, func(i, j int) bool { return tc.want[i].ObjectID < tc.want[j].ObjectID })
			roots := slices.Clone(tc.roots)
			got := access.OwnershipRelationLocks(view, tc.roots, tc.sources)
			if !slices.Equal(got, tc.want) || !slices.Equal(tc.roots, roots) {
				t.Fatal("exact sorted/deduplicated admission, without survivor companions", got, tc.want)
			}
			if e.Catalog.State != base || view.MustRelation(survivor) != original || !reflect.DeepEqual(original.Security.Policies, retained) {
				t.Fatal("lock enumeration changed retained catalog/policy roots")
			}
		})
	}
}

func objectACLUnitEntry(source, target authorization.PrincipalRef, use, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: source, Grantee: authorization.ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}
