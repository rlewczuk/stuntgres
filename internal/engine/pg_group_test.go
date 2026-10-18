package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgGroupUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12008}
	d, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing pg_group")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
}

func pgGroupUnitRole(e *Engine, state *roleCatalog, id uint64, name string, login bool) authorization.PrincipalRef {
	ref := pgRolesUnitRole(e, state, id, name)
	role := fixtureRole(state, ref)
	role.Login = login
	putFixtureRole(state, role)
	return ref
}

func pgGroupUnitRows(t *testing.T, source *querySource, d *catalog.Relation) map[uint32][]sqltype.Value {
	t.Helper()
	if source == nil {
		t.Fatal("missing capture")
	}
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.anonymous {
		if len(row) != 3 {
			t.Fatal("group width", len(row))
		}
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type || value.IsNull() {
				t.Fatal("group type/null", i)
			}
		}
		id, err := row[1].OID()
		if err != nil || bag[id] != nil {
			t.Fatal("group identity", id, err)
		}
		bag[id] = row
	}
	return bag
}

func pgGroupUnitMembers(t *testing.T, row []sqltype.Value, want ...uint32) {
	t.Helper()
	if len(row) != 3 {
		t.Fatal("missing group")
	}
	array := row[2]
	if array.IsNull() || array.Type().ID() != 1028 || array.ArrayLen() != len(want) {
		t.Fatal("nonnull OID[] shape", array.ArrayLen(), len(want))
	}
	dimensions := array.ArrayDimensions()
	if len(want) == 0 {
		if len(dimensions) != 0 {
			t.Fatal("empty array has dimensions")
		}
	} else if !slices.Equal(dimensions, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(want))}}) {
		t.Fatal("array rank/lower bound", dimensions)
	}
	got := make([]uint32, array.ArrayLen())
	for i := range got {
		element := array.ArrayElement(i)
		id, err := element.OID()
		if err != nil || element.IsNull() || element.Type() != array.Type().Element() {
			t.Fatal("typed nonnull element", err)
		}
		got[i] = id
	}
	slices.Sort(got)
	expected := slices.Clone(want)
	slices.Sort(expected)
	if !slices.Equal(got, expected) {
		t.Fatal("direct edge bag", got, expected)
	}
}

func TestPGGroupDescriptor(t *testing.T) {
	e := New()
	d, _ := pgGroupUnitSource(t, e)
	if d.Name() != "pg_group" || !d.VirtualView || d.View != nil || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || len(d.Columns) != 3 || len(d.Attributes) != 3 || len(d.Index) != 3 || d.AttributeHighWater != 3 {
		t.Fatal("descriptor shape")
	}
	for i, typ := range []sqltype.ID{sqltype.Name, sqltype.Oid, 1028} {
		c := d.Columns[i]
		name := []string{"groname", "grosysid", "grolist"}[i]
		collation := []uint32{950, 0, 0}[i]
		current, ok := e.CatalogRead().LookupType(typ)
		if !ok || c.Name != name || c.Usage.Type != current || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12008 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
			t.Fatal("exact group metadata", i, c)
		}
	}
	if !d.ACL.Explicit || len(d.ACL.Entries) != 1 || !d.ACL.Entries[0].Grantee.Public || d.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.ACL.Entries[0].Grantor != d.Owner || d.Owner != e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
		t.Fatal("PUBLIC SELECT/owner")
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	row := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12012})
	array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12011})
	if d.RowType != 12012 || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Array != 12011 || row.Owner != d.Owner || array.Owner != d.Owner || row.SQLType.Concrete() {
		t.Fatal("existing row/array ownership links")
	}
	if representedSummaryTable(12008) || representedSummaryViewDefinition(12008) == "" {
		t.Fatal("view classification")
	}
}

func TestPGGroupProjection(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	state := emptyRoleFixture(e)
	parent := pgGroupUnitRole(e, state, 20000, "parent", false)
	middle := pgGroupUnitRole(e, state, 20001, "middle", false)
	leaf := pgGroupUnitRole(e, state, 20002, "leaf", true)
	pgGroupUnitRole(e, state, 20003, "empty", false)
	max := pgGroupUnitRole(e, state, math.MaxUint32, "max", false)
	high := pgGroupUnitRole(e, state, uint64(1)<<32|20000, "high", false)
	highMember := pgGroupUnitRole(e, state, uint64(1)<<32|20002, "high-member", true)
	setFixtureEdges(state, []authorization.Membership{
		{Role: parent, Member: middle, Grantor: parent},
		{Role: middle, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: middle, Admin: true, Inherit: true, Set: true},
		{Role: parent, Member: max, Grantor: parent},
		{Role: high, Member: middle, Grantor: parent},
		{Role: parent, Member: highMember, Grantor: parent},
	})
	role := fixtureRole(state, parent)
	role.Superuser, role.CreateRole, role.CreateDB, role.Replication, role.BypassRLS = true, true, true, true, true
	role.Inherit, role.ConnectionLimit, role.HasValidUntil, role.ValidUntil = false, 0, true, sqltype.TimestampNegativeInfinity
	putFixtureRole(state, role)
	capture := func(root *roleCatalog) map[uint32][]sqltype.Value {
		t.Helper()
		source, err := capturePGGroup(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), root), ref, d)
		if err != nil {
			t.Fatal(err)
		}
		return pgGroupUnitRows(t, source, d)
	}
	bag := capture(state)
	if len(bag) != 4 {
		t.Fatal("NOLOGIN/provenance/uint32 filter", len(bag))
	}
	pgGroupUnitMembers(t, bag[20000], 20001, 20002, 20002, math.MaxUint32)
	pgGroupUnitMembers(t, bag[20001], 20002)
	pgGroupUnitMembers(t, bag[20003])
	pgGroupUnitMembers(t, bag[math.MaxUint32])
	if name, _ := bag[20000][0].Text(); name != "parent" {
		t.Fatal("group name")
	}
	changed := cloneRoleFixture(state)
	setFixtureEdges(changed, nil)
	replacement := fixtureRole(changed, parent)
	replacement.Login = true
	putFixtureRole(changed, replacement)
	if capture(changed)[20000] != nil {
		t.Fatal("current LOGIN still projected")
	}
	pgGroupUnitMembers(t, bag[20000], 20001, 20002, 20002, math.MaxUint32)
	setFixtureEdges(changed, fixtureEdges(state))
	retireFixtureRole(changed, leaf)
	pgGroupUnitMembers(t, capture(changed)[20001])
	retireFixtureRole(changed, parent)
	if capture(changed)[20000] != nil {
		t.Fatal("high or foreign identity aliases deleted role")
	}
	for _, slot := range []int{1, 2} {
		invalid := *d
		invalid.Columns = slices.Clone(d.Columns)
		invalid.Columns[slot].Usage.Type = testUsage(e, sqltype.Int4).Type
		if source, err := capturePGGroup(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), state), ref, &invalid); err == nil || source != nil {
			t.Fatal("constructor failure published partial source", slot)
		}
	}
	// A valid array descriptor with the wrong element type is not coerced.
	invalid := *d
	invalid.Columns = slices.Clone(d.Columns)
	invalid.Columns[2].Usage.Type = testUsage(e, sqltype.ID(1007)).Type
	if source, err := capturePGGroup(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), state), ref, &invalid); err == nil || source != nil {
		t.Fatal("wrong element descriptor accepted")
	}
}

func TestPGGroupRoleRoot(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	old := cloneRoleFixture(e.roles)
	group := pgGroupUnitRole(e, old, 20000, "snapshot", false)
	member := pgGroupUnitRole(e, old, 20001, "member", true)
	setFixtureEdges(old, append(fixtureEdges(old), authorization.Membership{Role: group, Member: member, Grantor: group}))
	e.roles = old
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.session.transactions}
	capture := func(s *StatementScope) {
		t.Helper()
		e.mu.Lock()
		err := e.captureCommentSnapshotLocked(s)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	open := func(s *StatementScope, r ids.ObjectRef) *querySource {
		t.Helper()
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(s, r, e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), queryRowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	capture(scope)
	first := open(scope, ref)
	updated := cloneRoleFixture(old)
	setFixtureEdges(updated, nil)
	e.roles = updated
	second := open(scope, ref)
	for _, source := range []*querySource{first, second} {
		pgGroupUnitMembers(t, pgGroupUnitRows(t, source, d)[20000], 20001)
	}
	addView := func(id ids.OID, name string, leaf ids.ObjectRef) ids.ObjectRef {
		view := *d
		view.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		view.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		view.ObjectName, view.RelationKind, view.VirtualView = name, catalog.ViewRelation, false
		attrs := slices.Clone(d.Attributes)
		view.View = &viewDefinition{query: &BoundQuery{Root: &sourceNode{querySchema: querySchema(d.Columns), ref: leaf}}, sources: []viewLeaf{{ref: leaf, attributes: attrs, reads: attrs}}}
		e.catalog.state.SetDescriptor(view.Address(), &view)
		return e.catalog.Domain.Ref(e.catalog.state.Epoch(), view.Address())
	}
	inner := addView(20010, "pg_group_inner", ref)
	outer := addView(20011, "pg_group_outer", inner)
	nested := open(scope, outer)
	defer nested.close()
	if nested.nestedInputs == nil || nested.nestedInputs.nestedInputs == nil {
		t.Fatal("nested source not opened")
	}
	pgGroupUnitMembers(t, pgGroupUnitRows(t, nested.nestedInputs.nestedInputs, d)[20000], 20001)
	fresh := &StatementScope{op: op, owner: op.session.transactions}
	capture(fresh)
	pgGroupUnitMembers(t, pgGroupUnitRows(t, open(fresh, ref), d)[20000])
	if scope.commentRoles != e.roleRead(scope.commentCatalog, old) || fresh.commentRoles != e.roleRead(fresh.commentCatalog, updated) {
		t.Fatal("role capture owner changed")
	}
	pgGroupUnitMembers(t, pgGroupUnitRows(t, first, d)[20000], 20001)
}

func TestPGGroupCancellation(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	state := emptyRoleFixture(e)
	group := pgGroupUnitRole(e, state, 20000, "group", false)
	member := pgGroupUnitRole(e, state, 20001, "member", true)
	for i := range 8 {
		grantor := pgGroupUnitRole(e, state, uint64(20002+i), fmt.Sprintf("grantor_%d", i), true)
		setFixtureEdges(state, append(fixtureEdges(state), authorization.Membership{Role: group, Member: member, Grantor: grantor}))
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, root := range []*roleCatalog{state, emptyRoleFixture(e)} {
		if source, err := capturePGGroup(canceled, e.roleRead(e.catalog.read(e.catalog.state), root), ref, d); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("precancellation published source", err)
		}
	}
	// Err calls: admission, roles, first edges, group allocation, second edges,
	// emission, final. Cancel deterministically within each meaningful pass.
	for _, at := range []int{2, 12, 20, 21, 29, 30} {
		ctx := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: at}
		if source, err := capturePGGroup(ctx, e.roleRead(e.catalog.read(e.catalog.state), state), ref, d); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("mid-pass cancellation published partial source", at, err)
		}
		if len(fixtureEdges(state)) != 8 || state.root.Read(authorization.PrincipalRef{}).RoleCount() != 10 {
			t.Fatal("cancellation mutated role root")
		}
	}
	source, err := capturePGGroup(context.Background(), e.roleRead(e.catalog.read(e.catalog.state), state), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	pgGroupUnitMembers(t, pgGroupUnitRows(t, source, d)[20000], 20001, 20001, 20001, 20001, 20001, 20001, 20001, 20001)
}
