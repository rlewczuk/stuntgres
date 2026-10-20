package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgShDescriptionUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2396}
	d, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing shared catalog descriptor")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
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
	uncommented := cloneRoleFixture(e.roles.published)
	pgShDescriptionUnitRole(e, uncommented, 20000, "uncommented", "")
	highOnly := cloneRoleFixture(e.roles.published)
	pgShDescriptionUnitRole(e, highOnly, uint64(math.MaxUint32)+1, "unrepresentable", "excluded")
	deleted := cloneRoleFixture(e.roles.published)
	removed := pgShDescriptionUnitRole(e, deleted, 20000, "removed", "old")
	retireFixtureRole(deleted, removed)
	root := &plan.ProjectNode{
		QuerySchema: plan.QuerySchema(d.Columns),
		Child:       &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: ref},
	}
	for i, column := range d.Columns {
		root.Expressions = append(root.Expressions, plan.ColumnExpr{
			Info:   expr.Info{Usage: column.Usage},
			Origin: ref, Scope: 1, Frame: 1, Slot: i,
		})
	}
	for _, tc := range []struct {
		name  string
		state *access.RoleRecord
	}{
		{"bootstrap", e.roles.published},
		{"empty role map", emptyRoleFixture(e)},
		{"uncommented role", uncommented},
		{"only unrepresentable comment", highOnly},
		{"dropped commented role", deleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), tc.state), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
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
			if count != 0 || !reflect.DeepEqual(rows.Schema(), d.Columns) {
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

func pgShDescriptionUnitRole(e *Engine, state *access.RoleRecord, id uint64, name, text string) authorization.PrincipalRef {
	e.roles.nextPrincipal = id
	ref, err := e.roles.principalCandidateLocked()
	if err != nil {
		panic(err)
	}
	e.roles.nextPrincipal = ref.ID() + 1
	putFixtureRole(state, authorization.RoleDescriptor{Ref: ref, Name: name, Comment: text})

	return ref
}

func pgShDescriptionUnitInquiry(t *testing.T, e *Engine, roles authorization.Read, catalog catalog.Read, id uint32, want *string) {
	t.Helper()
	oidUsage, nameUsage, textUsage := testUsage(e, sqltype.Oid), testUsage(e, sqltype.Name), testUsage(e, sqltype.Text)
	oid, err := sqltype.OIDValue(oidUsage.Type, id)
	if err != nil {
		t.Fatal(err)
	}
	expr := functions.CommentInquiry{Info: expr.Info{Usage: textUsage}, Kind: functions.SharedDescription, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: oidUsage}, Value: oid},
		plan.LiteralExpr{Info: expr.Info{Usage: nameUsage}, Value: sqltype.String(nameUsage.Type, "pg_authid")},
	}}
	value, err := expr.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), host: hostOf(&StatementScope{inquiry: inquirySnapshot{commentRoles: roles, commentCatalog: catalog}})}})
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
	if d.Name() != "pg_shdescription" || d.Address().ObjectID != 2396 || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 3 || len(d.Columns) != 3 || len(d.Attributes) != 3 || len(d.Index) != 3 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name      string
		typ       sqltype.ID
		collation uint32
	}{{"objoid", sqltype.Oid, 0}, {"classoid", sqltype.Oid, 0}, {"description", sqltype.Text, 950}} {
		c := d.Columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != tc.collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2396 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[tc.name] != i {
			t.Fatal("exact shared descriptor", i, c)
		}
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.Security.ACL.Entries[0].Grantor != d.Owner {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.Roles(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if err != nil || len(pgShDescriptionUnitBag(t, captured)) != 0 {
		t.Fatal("no invented bootstrap comment", err)
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ids.ObjectRef{}, class)
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
		kind, _ := row[class.Index["relkind"]].Text()
		natts, _ := row[class.Index["relnatts"]].Integer()
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
	state := cloneRoleFixture(e.roles.published)
	low := pgShDescriptionUnitRole(e, state, 20000, "low", "low shared")
	highID := uint64(1)<<32 | 20000
	high := pgShDescriptionUnitRole(e, state, highID, "high", "high excluded")
	pgShDescriptionUnitRole(e, state, math.MaxUint32, "max", "max shared")
	pgShDescriptionUnitRole(e, state, uint64(math.MaxUint32)+1, "overflow", "overflow excluded")
	pgShDescriptionUnitRole(e, state, 20001, "empty", "")
	pgShDescriptionUnitRole(e, state, 20002, "space", " ")
	e.roles.nextPrincipal = highID + 1
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), state)
	captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: roles, Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	want := map[uint32]string{20000: "low shared", math.MaxUint32: "max shared", 20002: " "}
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), want) {
		t.Fatal("exact width projection", captured, err)
	}
	pgShDescriptionUnitInquiry(t, e, roles, e.CatalogRead(), 20000, new("low shared"))
	removed := cloneRoleFixture(state)
	retireFixtureRole(removed, low)
	captured, err = captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), removed), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	delete(want, 20000)
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), want) {
		t.Fatal("high principal resurrected low", err)
	}
	pgShDescriptionUnitInquiry(t, e, e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), removed), e.CatalogRead(), 20000, nil)
	if e.roles.nextPrincipal != highID+1 || fixtureRole(state, high).Ref.ID() != highID || fixtureRole(state, low).Comment != "low shared" {
		t.Fatal("projection changed uint64 identities")
	}
}

func TestPGShDescriptionCapturedRows(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	old := cloneRoleFixture(e.roles.published)
	role := pgShDescriptionUnitRole(e, old, 20000, "same_name", "old text")
	snapshot := catalog.CloneState(e.catalog.state)
	local := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	snapshot.SetDescriptor(local, &catalog.Relation{ObjectAddress: local, Comment: "local collision"})
	read := e.catalog.read(snapshot)
	captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.read(e.catalog.owner(), read, old), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneRoleFixture(old)
	replacement := fixtureRole(updated, role)
	replacement.Comment = "replacement"
	putFixtureRole(updated, replacement)
	e.roles.published = updated
	fresh, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.Roles(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if err != nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, fresh), map[uint32]string{20000: "replacement"}) {
		t.Fatal(err)
	}
	pgShDescriptionUnitInquiry(t, e, e.roles.read(e.catalog.owner(), read, old), read, 20000, new("old text"))
	pgShDescriptionUnitInquiry(t, e, e.roles.read(e.catalog.owner(), read, e.roles.published), read, 20000, new("replacement"))
	deleted := cloneRoleFixture(updated)
	retireFixtureRole(deleted, role)
	pgShDescriptionUnitRole(e, deleted, 20001, "same_name", "")
	e.roles.published = deleted
	fresh, err = captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.Roles(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if err != nil || len(pgShDescriptionUnitBag(t, fresh)) != 0 || !reflect.DeepEqual(pgShDescriptionUnitBag(t, captured), map[uint32]string{20000: "old text"}) {
		t.Fatal("detached rows or same-name inheritance", err)
	}
	pgShDescriptionUnitInquiry(t, e, e.roles.read(e.catalog.owner(), read, e.roles.published), read, 20000, nil)
	pgShDescriptionUnitInquiry(t, e, e.roles.read(e.catalog.owner(), read, e.roles.published), read, 20001, nil)
}

func TestPGShDescriptionScopeSnapshot(t *testing.T) {
	e := New()
	d, ref := pgShDescriptionUnitSource(t, e)
	old := cloneRoleFixture(e.roles.published)
	role := pgShDescriptionUnitRole(e, old, 20000, "snapshot", "old")
	e.roles.published = old
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.session.transactions}
	e.mu.Lock()
	err := e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneRoleFixture(old)
	replacement := fixtureRole(updated, role)
	replacement.Comment = "new"
	putFixtureRole(updated, replacement)
	e.roles.published = updated
	open := func(scope *StatementScope, ref ids.ObjectRef) *querySource {
		t.Helper()
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(scope, ref, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	if !reflect.DeepEqual(pgShDescriptionUnitBag(t, open(scope, ref)), map[uint32]string{20000: "old"}) {
		t.Fatal("opening resampled live roles")
	}
	// Construct typed immutable nested views, then use the actual relocking seam.
	addView := func(id ids.OID, name string, leaf ids.ObjectRef) ids.ObjectRef {
		view := *d
		view.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		view.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		view.ObjectName, view.RelationKind = name, catalog.ViewRelation
		viewRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), view.Address())
		view.View = &viewDefinition{query: &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: leaf}}, sources: []viewLeaf{{ref: leaf, attributes: []int32{1, 2, 3}, reads: []int32{1, 2, 3}}}}
		e.catalog.state.SetDescriptor(view.Address(), &view)
		return viewRef
	}
	inner := addView(20010, "shared_inner", ref)
	outer := addView(20011, "shared_outer", inner)
	nested := open(scope, outer)
	defer nested.close()
	if nested.nestedInputs == nil || nested.nestedInputs.nestedInputs == nil || !reflect.DeepEqual(pgShDescriptionUnitBag(t, nested.nestedInputs.nestedInputs), map[uint32]string{20000: "old"}) {
		t.Fatal("nested opening lost saved roles")
	}
	pgShDescriptionUnitInquiry(t, e, scope.inquiry.commentRoles, scope.inquiry.commentCatalog, 20000, new("old"))
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
	state := cloneRoleFixture(e.roles.published)
	pgShDescriptionUnitRole(e, state, 20000, "first", "first")
	pgShDescriptionUnitRole(e, state, 20001, "second", "second")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, roles := range []authorization.Read{e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), emptyRoleFixture(e)), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), state)} {
		captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Roles: roles, Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("entry cancellation published source", captured, err)
		}
	}
	inLoop := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: 3}
	captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: inLoop, Roles: e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), state), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if captured != nil || !errors.Is(err, context.Canceled) || inLoop.calls != 3 {
		t.Fatal("in-loop cancellation published partial source", captured, err, inLoop.calls)
	}
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[0].Usage.Type = testUsage(e, sqltype.Int4).Type
	captured, err = captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), state), Ref: ref, Source: &invalid, Runtime: engineCatalogRuntime{}}, capturePGShDescription)
	if captured != nil || err == nil {
		t.Fatal("OID constructor failure published partial source", captured, err)
	}
}
