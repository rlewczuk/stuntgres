package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgShDescriptionUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 2396}
	d, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok {
		t.Fatal("missing shared catalog descriptor")
	}
	return d, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: address}
}

func pgShDescriptionUnitBag(t *testing.T, source *querySource) map[uint32]string {
	t.Helper()
	if source == nil {
		t.Fatal("missing captured source")
	}
	bag := make(map[uint32]string)
	for _, row := range source.anonymous {
		if len(row) != 3 || row[0].Type().ID() != sqltype.Oid || row[1].Type().ID() != sqltype.Oid || row[2].Type().ID() != sqltype.Text {
			t.Fatal("shared row types", row)
		}
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		class, err := row[1].OID()
		if err != nil || class != 1260 {
			t.Fatal(class, err)
		}
		text, err := row[2].Text()
		if err != nil || text == "" {
			t.Fatal(text, err)
		}
		if _, exists := bag[id]; exists {
			t.Fatal("duplicate shared identity", id)
		}
		bag[id] = text
	}
	return bag
}

func TestPGShDescriptionEmptySource(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	uncommented := cloneRoles(e.roles)
	pgShDescriptionUnitRole(e, uncommented, 20000, "uncommented", "")
	highOnly := cloneRoles(e.roles)
	pgShDescriptionUnitRole(e, highOnly, uint64(math.MaxUint32)+1, "unrepresentable", "excluded")
	deleted := cloneRoles(e.roles)
	removed := pgShDescriptionUnitRole(e, deleted, 20000, "removed", "old")
	delete(deleted.roles, removed)
	delete(deleted.names, "removed")
	root := &projectNode{
		querySchema: querySchema(d.columns),
		child:       &sourceNode{querySchema: querySchema(d.columns), ref: ref},
	}
	for i, column := range d.columns {
		root.expressions = append(root.expressions, columnExpression{
			expressionInfo: expressionInfo{usage: column.Usage},
			origin:         ref, scope: 1, frame: 1, slot: i,
		})
	}
	for _, tc := range []struct {
		name  string
		state *roleCatalog
	}{
		{"bootstrap", e.roles},
		{"empty role map", &roleCatalog{roles: map[PrincipalRef]RoleDescriptor{}}},
		{"uncommented role", uncommented},
		{"only unrepresentable comment", highOnly},
		{"dropped commented role", deleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := capturePGShDescription(context.Background(), RoleRead{owner: e, state: tc.state, catalog: CatalogRead{e, e.state}}, ref, d)
			if err != nil {
				t.Fatal(err)
			}
			if source.anonymous == nil || len(source.anonymous) != 0 {
				t.Fatal("empty catalog must remain an anonymous source", source)
			}
			rows, count, err := finalizeQuery(context.Background(), root, source, nil)
			if err != nil {
				t.Fatal("empty captured source must execute", err)
			}
			defer rows.Close()
			if count != 0 || !reflect.DeepEqual(rows.Schema(), d.columns) {
				t.Fatal("empty result shape/count", rows.Schema(), count)
			}
			if present, err := rows.Next(context.Background(), make([]sqltype.Value, 3)); present || err != nil {
				t.Fatal("empty result produced a row", present, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func pgShDescriptionUnitRole(e *Engine, state *roleCatalog, id uint64, name, text string) PrincipalRef {
	ref := PrincipalRef{owner: e, id: id}
	state.roles[ref] = RoleDescriptor{Ref: ref, Name: name, Comment: text}
	state.names[name] = ref
	return ref
}

func pgShDescriptionUnitInquiry(t *testing.T, e *Engine, roles RoleRead, catalog CatalogRead, id uint32, want *string) {
	t.Helper()
	oidUsage, nameUsage, textUsage := testUsage(e, sqltype.Oid), testUsage(e, sqltype.Name), testUsage(e, sqltype.Text)
	oid, err := sqltype.OIDValue(oidUsage.Type, id)
	if err != nil {
		t.Fatal(err)
	}
	expr := commentInquiryExpression{expressionInfo: expressionInfo{usage: textUsage}, kind: sharedDescription, args: []BoundExpr{
		literalExpression{expressionInfo: expressionInfo{usage: oidUsage}, value: oid},
		literalExpression{expressionInfo: expressionInfo{usage: nameUsage}, value: sqltype.String(nameUsage.Type, "pg_authid")},
	}}
	value, err := expr.Eval(&EvalContext{Context: context.Background(), statementScope: &StatementScope{commentRoles: roles, commentCatalog: catalog}})
	if err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if !value.IsNull() {
			t.Fatal("missing identity inquiry", value)
		}
		return
	}
	text, err := value.Text()
	if err != nil || text != *want {
		t.Fatal("saved inquiry", text, want, err)
	}
}

func TestPGShDescriptionDescriptor(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	if d.name != "pg_shdescription" || d.address.ObjectID != 2396 || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.attributeHighWater != 3 || len(d.columns) != 3 || len(d.attributes) != 3 || len(d.index) != 3 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name      string
		typ       sqltype.ID
		collation uint32
	}{{"objoid", sqltype.Oid, 0}, {"classoid", sqltype.Oid, 0}, {"description", sqltype.Text, 950}} {
		c := d.columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != tc.collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2396 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[tc.name] != i {
			t.Fatal("exact shared descriptor", i, c)
		}
	}
	if !d.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != SelectPrivilege || d.acl.Entries[0].Grantor != d.owner {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	captured, err := capturePGShDescription(context.Background(), e.Roles(), ref, d)
	if err != nil || len(pgShDescriptionUnitBag(t, captured)) != 0 {
		t.Fatal("no invented bootstrap comment", err)
	}
	class := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 1259}].(*relationDescriptor)
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), RoleRead{e, e.roles, CatalogRead{e, e.state}}, ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows.anonymous {
		oid, _ := row[0].OID()
		if oid != 2396 {
			continue
		}
		name, _ := row[1].Text()
		namespace, _ := row[2].OID()
		kind, _ := row[4].Text()
		natts, _ := row[5].Integer()
		if name != "pg_shdescription" || namespace != 11 || kind != "r" || natts != 3 {
			t.Fatal(row)
		}
		found++
	}
	if found != 1 {
		t.Fatal("pg_class shared descriptor multiplicity", found)
	}
}

func TestPGShDescriptionIdentityWidth(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	state := cloneRoles(e.roles)
	low := pgShDescriptionUnitRole(e, state, 20000, "low", "low shared")
	highID := uint64(1)<<32 | 20000
	high := pgShDescriptionUnitRole(e, state, highID, "high", "high excluded")
	pgShDescriptionUnitRole(e, state, math.MaxUint32, "max", "max shared")
	pgShDescriptionUnitRole(e, state, uint64(math.MaxUint32)+1, "overflow", "overflow excluded")
	pgShDescriptionUnitRole(e, state, 20001, "empty", "")
	pgShDescriptionUnitRole(e, state, 20002, "space", " ")
	e.nextPrincipal = highID + 1
	roles := RoleRead{owner: e, state: state, catalog: CatalogRead{e, e.state}}
	captured, err := capturePGShDescription(context.Background(), roles, ref, d)
	want := map[uint32]string{20000: "low shared", math.MaxUint32: "max shared", 20002: " "}
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), want) {
		t.Fatal("exact width projection", captured, err)
	}
	pgShDescriptionUnitInquiry(t, e, roles, e.CatalogRead(), 20000, new("low shared"))
	removed := cloneRoles(state)
	delete(removed.roles, low)
	delete(removed.names, "low")
	captured, err = capturePGShDescription(context.Background(), RoleRead{owner: e, state: removed, catalog: CatalogRead{e, e.state}}, ref, d)
	delete(want, 20000)
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), want) {
		t.Fatal("high principal resurrected low", err)
	}
	pgShDescriptionUnitInquiry(t, e, RoleRead{owner: e, state: removed, catalog: CatalogRead{e, e.state}}, e.CatalogRead(), 20000, nil)
	if e.nextPrincipal != highID+1 || state.roles[high].Ref.ID() != highID || state.roles[low].Comment != "low shared" {
		t.Fatal("projection changed uint64 identities")
	}
}

func TestPGShDescriptionCapturedRows(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	old := cloneRoles(e.roles)
	role := pgShDescriptionUnitRole(e, old, 20000, "same_name", "old text")
	catalog := cloneCatalog(e.state)
	local := ObjectAddress{ClassID: RelationClassID, ObjectID: 20000}
	catalog.descriptors[local] = &relationDescriptor{address: local, comment: "local collision"}
	read := CatalogRead{owner: e, state: catalog}
	captured, err := capturePGShDescription(context.Background(), RoleRead{owner: e, state: old, catalog: read}, ref, d)
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneRoles(old)
	replacement := updated.roles[role]
	replacement.Comment = "replacement"
	updated.roles[role] = replacement
	e.roles = updated
	fresh, err := capturePGShDescription(context.Background(), e.Roles(), ref, d)
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, fresh), map[uint32]string{20000: "replacement"}) {
		t.Fatal(err)
	}
	pgShDescriptionUnitInquiry(t, e, RoleRead{owner: e, state: old, catalog: read}, read, 20000, new("old text"))
	pgShDescriptionUnitInquiry(t, e, RoleRead{e, e.roles, read}, read, 20000, new("replacement"))
	deleted := cloneRoles(updated)
	delete(deleted.roles, role)
	delete(deleted.names, "same_name")
	pgShDescriptionUnitRole(e, deleted, 20001, "same_name", "")
	e.roles = deleted
	fresh, err = capturePGShDescription(context.Background(), e.Roles(), ref, d)
	if err != nil || len(pgShDescriptionUnitBag(t, fresh)) != 0 || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), map[uint32]string{20000: "old text"}) {
		t.Fatal("detached rows or same-name inheritance", err)
	}
	pgShDescriptionUnitInquiry(t, e, RoleRead{e, e.roles, read}, read, 20000, nil)
	pgShDescriptionUnitInquiry(t, e, RoleRead{e, e.roles, read}, read, 20001, nil)
}

func TestPGShDescriptionScopeSnapshot(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	old := cloneRoles(e.roles)
	role := pgShDescriptionUnitRole(e, old, 20000, "snapshot", "old")
	e.roles = old
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.session.transactions}
	e.mu.Lock()
	err := e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneRoles(old)
	replacement := updated.roles[role]
	replacement.Comment = "new"
	updated.roles[role] = replacement
	e.roles = updated
	open := func(scope *StatementScope, ref ObjectRef) *querySource {
		t.Helper()
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(scope, ref, e.roles.names["postgres"], queryRowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	if !reflect.DeepEqual(pgShDescriptionUnitBag(t, open(scope, ref)), map[uint32]string{20000: "old"}) {
		t.Fatal("opening resampled live roles")
	}
	// Construct typed immutable nested views, then use the actual relocking seam.
	addView := func(id OID, name string, leaf ObjectRef) ObjectRef {
		view := *d
		view.address = ObjectAddress{ClassID: RelationClassID, ObjectID: id}
		view.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
		view.name, view.relationKind = name, viewRelation
		viewRef := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: view.address}
		view.view = &viewDefinition{query: &BoundQuery{Root: &sourceNode{querySchema: querySchema(d.columns), ref: leaf}}, sources: []viewLeaf{{ref: leaf, attributes: []int32{1, 2, 3}, reads: []int32{1, 2, 3}}}}
		e.state.descriptors[view.address] = &view
		return viewRef
	}
	inner := addView(20010, "shared_inner", ref)
	outer := addView(20011, "shared_outer", inner)
	nested := open(scope, outer)
	defer nested.close()
	if nested.nestedInputs == nil || nested.nestedInputs.nestedInputs == nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, nested.nestedInputs.nestedInputs), map[uint32]string{20000: "old"}) {
		t.Fatal("nested opening lost saved roles")
	}
	pgShDescriptionUnitInquiry(t, e, scope.commentRoles, scope.commentCatalog, 20000, new("old"))
	fresh := &StatementScope{op: op, owner: op.session.transactions}
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(fresh)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgShDescriptionUnitBag(t, open(fresh, ref)), map[uint32]string{20000: "new"}) {
		t.Fatal("fresh scope retained old roles")
	}
}

type pgShDescriptionCancelContext struct {
	context.Context
	calls, cancelAt int
}

func (c *pgShDescriptionCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestPGShDescriptionCanceledCapture(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	state := cloneRoles(e.roles)
	pgShDescriptionUnitRole(e, state, 20000, "first", "first")
	pgShDescriptionUnitRole(e, state, 20001, "second", "second")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, roles := range []RoleRead{{owner: e, state: &roleCatalog{roles: map[PrincipalRef]RoleDescriptor{}}, catalog: CatalogRead{e, e.state}}, {owner: e, state: state, catalog: CatalogRead{e, e.state}}} {
		captured, err := capturePGShDescription(ctx, roles, ref, d)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("entry cancellation published source", captured, err)
		}
	}
	inLoop := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: 3}
	captured, err := capturePGShDescription(inLoop, RoleRead{owner: e, state: state, catalog: CatalogRead{e, e.state}}, ref, d)
	if captured != nil || !errors.Is(err, context.Canceled) || inLoop.calls != 3 {
		t.Fatal("in-loop cancellation published partial source", captured, err, inLoop.calls)
	}
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type = testUsage(e, sqltype.Int4).Type
	captured, err = capturePGShDescription(context.Background(), RoleRead{owner: e, state: state, catalog: CatalogRead{e, e.state}}, ref, &invalid)
	if captured != nil || err == nil {
		t.Fatal("OID constructor failure published partial source", captured, err)
	}
}
