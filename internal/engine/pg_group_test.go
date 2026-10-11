package engine

import (
	"context"
	"errors"
	"math"
	"slices"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgGroupUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 12008}
	d, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_group")
	}
	return d, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: address}
}

func pgGroupUnitRole(e *Engine, state *roleCatalog, id uint64, name string, login bool) PrincipalRef {
	ref := pgRolesUnitRole(e, state, id, name)
	role := state.roles[ref]
	role.Login = login
	state.roles[ref] = role
	return ref
}

func pgGroupUnitRows(t *testing.T, source *querySource, d *relationDescriptor) map[uint32][]sqltype.Value {
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
			if value.Type() != d.columns[i].Usage.Type || value.IsNull() {
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
	if d.name != "pg_group" || !d.virtualView || d.view != nil || d.relationKind != catalogRelation || d.namespace.ObjectID != 11 || len(d.columns) != 3 || len(d.attributes) != 3 || len(d.index) != 3 || d.attributeHighWater != 3 {
		t.Fatal("descriptor shape")
	}
	for i, typ := range []sqltype.ID{sqltype.Name, sqltype.Oid, 1028} {
		c := d.columns[i]
		name := []string{"groname", "grosysid", "grolist"}[i]
		collation := []uint32{950, 0, 0}[i]
		current, ok := e.CatalogRead().LookupType(typ)
		if !ok || c.Name != name || c.Usage.Type != current || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12008 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i {
			t.Fatal("exact group metadata", i, c)
		}
	}
	if !d.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != SelectPrivilege || d.acl.Entries[0].Grantor != d.owner || d.owner != e.roles.names["postgres"] {
		t.Fatal("PUBLIC SELECT/owner")
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	row := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 12012}].(*typeDescriptor)
	array := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 12011}].(*typeDescriptor)
	if d.rowType != 12012 || row.owningRelation != d.address || array.owningRelation != d.address || row.array != 12011 || row.owner != d.owner || array.owner != d.owner || row.typ.Concrete() {
		t.Fatal("existing row/array ownership links")
	}
	if representedSummaryTable(12008) || representedSummaryViewDefinition(12008) == "" {
		t.Fatal("view classification")
	}
}

func TestPGGroupProjection(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	state := &roleCatalog{roles: make(map[PrincipalRef]RoleDescriptor), names: make(map[string]PrincipalRef)}
	parent := pgGroupUnitRole(e, state, 20000, "parent", false)
	middle := pgGroupUnitRole(e, state, 20001, "middle", false)
	leaf := pgGroupUnitRole(e, state, 20002, "leaf", true)
	pgGroupUnitRole(e, state, 20003, "empty", false)
	max := pgGroupUnitRole(e, state, math.MaxUint32, "max", false)
	high := pgGroupUnitRole(e, state, uint64(1)<<32|20000, "high", false)
	highMember := pgGroupUnitRole(e, state, uint64(1)<<32|20002, "high-member", true)
	foreign := PrincipalRef{owner: New(), id: leaf.id}
	state.roles[foreign] = RoleDescriptor{Ref: foreign, Name: "foreign"}
	stale := PrincipalRef{owner: e, id: 21000}
	state.edges = []Membership{
		{Role: parent, Member: middle, Grantor: parent},
		{Role: middle, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: middle, Admin: true, Inherit: true, Set: true},
		{Role: parent, Member: max, Grantor: parent},
		{Role: high, Member: middle, Grantor: parent},
		{Role: parent, Member: highMember, Grantor: parent},
		{Role: parent, Member: foreign, Grantor: parent},
		{Role: parent, Member: stale, Grantor: parent},
		{Role: PrincipalRef{owner: foreign.owner, id: parent.id}, Member: leaf, Grantor: parent},
	}
	role := state.roles[parent]
	role.Superuser, role.CreateRole, role.CreateDB, role.Replication, role.BypassRLS = true, true, true, true, true
	role.Inherit, role.ConnectionLimit, role.HasValidUntil, role.ValidUntil = false, 0, true, sqltype.TimestampNegativeInfinity
	state.roles[parent] = role
	capture := func(root *roleCatalog) map[uint32][]sqltype.Value {
		t.Helper()
		source, err := capturePGGroup(context.Background(), RoleRead{owner: e, state: root}, ref, d)
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
	changed := cloneRoles(state)
	changed.edges = nil
	replacement := changed.roles[parent]
	replacement.Login = true
	changed.roles[parent] = replacement
	if capture(changed)[20000] != nil {
		t.Fatal("current LOGIN still projected")
	}
	pgGroupUnitMembers(t, bag[20000], 20001, 20002, 20002, math.MaxUint32)
	delete(changed.roles, leaf)
	changed.edges = state.edges
	pgGroupUnitMembers(t, capture(changed)[20001])
	delete(changed.roles, parent)
	if capture(changed)[20000] != nil {
		t.Fatal("high or foreign identity aliases deleted role")
	}
	for _, slot := range []int{1, 2} {
		invalid := *d
		invalid.columns = slices.Clone(d.columns)
		invalid.columns[slot].Usage.Type = testUsage(e, sqltype.Int4).Type
		if source, err := capturePGGroup(context.Background(), RoleRead{owner: e, state: state}, ref, &invalid); err == nil || source != nil {
			t.Fatal("constructor failure published partial source", slot)
		}
	}
	// A valid array descriptor with the wrong element type is not coerced.
	invalid := *d
	invalid.columns = slices.Clone(d.columns)
	invalid.columns[2].Usage.Type = testUsage(e, sqltype.ID(1007)).Type
	if source, err := capturePGGroup(context.Background(), RoleRead{owner: e, state: state}, ref, &invalid); err == nil || source != nil {
		t.Fatal("wrong element descriptor accepted")
	}
}

func TestPGGroupRoleRoot(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	old := cloneRoles(e.roles)
	group := pgGroupUnitRole(e, old, 20000, "snapshot", false)
	member := pgGroupUnitRole(e, old, 20001, "member", true)
	old.edges = append(old.edges, Membership{Role: group, Member: member, Grantor: group})
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
	open := func(s *StatementScope, r ObjectRef) *querySource {
		t.Helper()
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(s, r, e.roles.names["postgres"], queryRowMark{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	capture(scope)
	first := open(scope, ref)
	updated := cloneRoles(old)
	updated.edges = nil
	e.roles = updated
	second := open(scope, ref)
	for _, source := range []*querySource{first, second} {
		pgGroupUnitMembers(t, pgGroupUnitRows(t, source, d)[20000], 20001)
	}
	addView := func(id OID, name string, leaf ObjectRef) ObjectRef {
		view := *d
		view.address = ObjectAddress{ClassID: RelationClassID, ObjectID: id}
		view.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
		view.name, view.relationKind, view.virtualView = name, viewRelation, false
		attrs := slices.Clone(d.attributes)
		view.view = &viewDefinition{query: &BoundQuery{Root: &sourceNode{querySchema: querySchema(d.columns), ref: leaf}}, sources: []viewLeaf{{ref: leaf, attributes: attrs, reads: attrs}}}
		e.state.descriptors[view.address] = &view
		return ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: view.address}
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
	if scope.commentRoles.state != old || fresh.commentRoles.state != updated {
		t.Fatal("role capture owner changed")
	}
	pgGroupUnitMembers(t, pgGroupUnitRows(t, first, d)[20000], 20001)
}

func TestPGGroupCancellation(t *testing.T) {
	e := New()
	d, ref := pgGroupUnitSource(t, e)
	state := &roleCatalog{roles: make(map[PrincipalRef]RoleDescriptor), names: make(map[string]PrincipalRef)}
	group := pgGroupUnitRole(e, state, 20000, "group", false)
	member := pgGroupUnitRole(e, state, 20001, "member", true)
	for range 8 {
		state.edges = append(state.edges, Membership{Role: group, Member: member, Grantor: group})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, root := range []*roleCatalog{state, {roles: map[PrincipalRef]RoleDescriptor{}}} {
		if source, err := capturePGGroup(canceled, RoleRead{owner: e, state: root}, ref, d); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("precancellation published source", err)
		}
	}
	// Err calls: admission, roles, first edges, group allocation, second edges,
	// emission, final. Cancel deterministically within each meaningful pass.
	for _, at := range []int{2, 5, 12, 15, 21, 22} {
		ctx := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: at}
		if source, err := capturePGGroup(ctx, RoleRead{owner: e, state: state}, ref, d); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("mid-pass cancellation published partial source", at, err)
		}
		if len(state.edges) != 8 || len(state.roles) != 2 {
			t.Fatal("cancellation mutated role root")
		}
	}
	source, err := capturePGGroup(context.Background(), RoleRead{owner: e, state: state}, ref, d)
	if err != nil {
		t.Fatal(err)
	}
	pgGroupUnitMembers(t, pgGroupUnitRows(t, source, d)[20000], 20001, 20001, 20001, 20001, 20001, 20001, 20001, 20001)
}
