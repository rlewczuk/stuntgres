package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

func dtpUnitAuthority(t *testing.T, f defaultUnitFixture, catalog CatalogRead) AuthorizationView {
	t.Helper()
	f.e.mu.Lock()
	auth, err := f.e.authorizationLocked(f.op)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.recaptureCatalog(catalog)
	return auth
}

func dtpUnitCapture(ctx context.Context, catalog CatalogRead, auth AuthorizationView) (*querySource, error) {
	source := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: dataTypePrivilegesOID}].(*relationDescriptor)
	ref := ObjectRef{owner: catalog.owner.domain, Epoch: catalog.Epoch(), Address: source.address}
	return captureDataTypePrivileges(ctx, catalog, auth, "unit_database", ref, source)
}

// A deterministic cancellation point inside enumeration, without timing races.
type dtpCancelContext struct {
	context.Context
	remaining int
}

func (c *dtpCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestDataTypePrivilegesCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	catalog := f.e.CatalogRead()
	auth := dtpUnitAuthority(t, f, catalog)
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 4}} {
		rows, err := dtpUnitCapture(ctx, catalog, auth)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed rows", rows, err)
		}
	}
	for _, change := range []func(*AuthorizationView){
		func(a *AuthorizationView) { a.engine = New() },
		func(a *AuthorizationView) { a.roleRecord = nil },
		func(a *AuthorizationView) { a.pairedCatalog = CatalogRead{} },
		func(a *AuthorizationView) { a.roles = New().roles.root.Read(authorization.PrincipalRef{}) },
		func(a *AuthorizationView) { a.state.CurrentUser = authorization.PrincipalRef{} },
	} {
		bad := auth
		change(&bad)
		if rows, err := dtpUnitCapture(context.Background(), catalog, bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	source := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: dataTypePrivilegesOID}].(*relationDescriptor)
	ref := ObjectRef{owner: f.e.domain, Epoch: catalog.Epoch(), Address: source.address}
	stale := ref
	stale.Epoch++
	if rows, err := captureDataTypePrivileges(context.Background(), catalog, auth, "unit_database", stale, source); rows != nil || err == nil {
		t.Fatal("stale ref accepted", rows, err)
	}
	copySource := *source
	if rows, err := captureDataTypePrivileges(context.Background(), catalog, auth, "unit_database", ref, &copySource); rows != nil || err == nil {
		t.Fatal("foreign source accepted", rows, err)
	}
	for i, column := range source.columns {
		want := sqltype.Name
		if i == 3 {
			want = sqltype.VarChar
		}
		if column.Usage.Type.ID() != want || column.Usage.Type.IsDomain() || column.OriginTable != uint32(dataTypePrivilegesOID) || column.OriginAttribute != int16(i+1) || source.attributes[i] != int32(i+1) || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("incorrect source carrier/origin", i, column)
		}
	}
	// A real staged user domain cannot inherit the seeded builtin exemption.
	domainUnitPublish(t, f.e, domainUnitStage(t, f.e, f.op, "CREATE DOMAIN public.dtp_ownerless AS integer"))
	state := cloneCatalog(f.e.state)
	domain := *domainUnitType(t, CatalogRead{f.e, state}, "dtp_ownerless")
	domain.owner = authorization.PrincipalRef{}
	state.descriptors[domain.Address()] = &domain
	badCatalog := CatalogRead{f.e, state}
	if rows, err := dtpUnitCapture(context.Background(), badCatalog, dtpUnitAuthority(t, f, badCatalog)); rows != nil || err == nil {
		t.Fatal("ownerless user domain disclosed", rows, err)
	}
}

func TestDataTypePrivilegesVisibility(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := cloneCatalog(f.e.state)
	typ, _ := (CatalogRead{f.e, state}).LookupType(sqltype.Int4)
	relation := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20110}, namespace: f.schema, name: "dtp_unit", owner: f.owner, revision: 1, relationKind: tableRelation, acl: authorization.ACLValue{Explicit: true}, attributes: []int32{1, 3, 4}, columns: []ColumnDescriptor{{Name: "a", Usage: typ.Usage()}, {Name: "c", Usage: typ.Usage()}, {Name: "d", Usage: typ.Usage()}}, columnACL: map[int32]authorization.ACLValue{3: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}}
	state.descriptors[relation.address] = relation
	catalog := CatalogRead{f.e, state}
	auth := dtpUnitAuthority(t, f, catalog)
	auth.state.CurrentUser = f.reader
	held, err := dtpUnitCapture(context.Background(), catalog, auth)
	if err != nil {
		t.Fatal(err)
	}
	selectIDs := func(source *querySource) []string {
		var ids []string
		for _, row := range source.anonymous {
			name, _ := row[2].Text()
			if name == "dtp_unit" {
				id, _ := row[4].Text()
				ids = append(ids, id)
			}
		}
		return ids
	}
	if got := selectIDs(held); !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatal("stable column-only identity", got)
	}
	before := make([][]sqltype.Value, len(held.anonymous))
	for i, row := range held.anonymous {
		before[i] = append([]sqltype.Value(nil), row...)
	}
	changed := cloneCatalog(state)
	revoked := *relation
	revoked.columnACL = nil
	changed.descriptors[relation.address] = &revoked
	auth.recaptureCatalog(CatalogRead{f.e, changed})
	fresh, err := dtpUnitCapture(context.Background(), auth.catalog, auth)
	if err != nil || len(selectIDs(fresh)) != 0 || !reflect.DeepEqual(before, held.anonymous) {
		t.Fatal("capture lifetime", err)
	}
	auth.state.CurrentUser = f.owner
	owned, err := dtpUnitCapture(context.Background(), auth.catalog, auth)
	if err != nil || !reflect.DeepEqual(selectIDs(owned), []string{"1", "3", "4"}) {
		t.Fatal("owner independent of ordinary ACL", err)
	}
	auth.state.CurrentUser = f.member
	inherited, err := dtpUnitCapture(context.Background(), auth.catalog, auth)
	if err != nil || !reflect.DeepEqual(selectIDs(inherited), []string{"1", "3", "4"}) {
		t.Fatal("inherited owner", err)
	}
}
