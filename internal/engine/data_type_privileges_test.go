package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func dtpUnitAuthority(t *testing.T, f defaultUnitFixture, catalog catalog.Read) AuthorizationView {
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

func dtpUnitCapture(ctx context.Context, catalog catalog.Read, auth AuthorizationView) (*querySource, error) {
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: dataTypePrivilegesOID})
	ref := catalog.Owner().Domain.Ref(catalog.Epoch(), source.Address())
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
	view := f.e.CatalogRead()
	auth := dtpUnitAuthority(t, f, view)
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 4}} {
		rows, err := dtpUnitCapture(ctx, view, auth)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed rows", rows, err)
		}
	}
	for _, change := range []func(*AuthorizationView){
		func(a *AuthorizationView) { a.engine = New() },
		func(a *AuthorizationView) { a.roleRecord = nil },
		func(a *AuthorizationView) { a.pairedCatalog = catalog.Read{} },
		func(a *AuthorizationView) { a.roles = New().roles.root.Read(authorization.PrincipalRef{}) },
		func(a *AuthorizationView) { a.state.CurrentUser = authorization.PrincipalRef{} },
	} {
		bad := auth
		change(&bad)
		if rows, err := dtpUnitCapture(context.Background(), view, bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: dataTypePrivilegesOID})
	ref := f.e.catalog.Domain.Ref(view.Epoch(), source.Address())
	stale := ref
	stale.Epoch++
	if rows, err := captureDataTypePrivileges(context.Background(), view, auth, "unit_database", stale, source); rows != nil || err == nil {
		t.Fatal("stale ref accepted", rows, err)
	}
	copySource := *source
	if rows, err := captureDataTypePrivileges(context.Background(), view, auth, "unit_database", ref, &copySource); rows != nil || err == nil {
		t.Fatal("foreign source accepted", rows, err)
	}
	for i, column := range source.Columns {
		want := sqltype.Name
		if i == 3 {
			want = sqltype.VarChar
		}
		if column.Usage.Type.ID() != want || column.Usage.Type.IsDomain() || column.OriginTable != uint32(dataTypePrivilegesOID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("incorrect source carrier/origin", i, column)
		}
	}
	// A real staged user domain cannot inherit the seeded builtin exemption.
	domainUnitPublish(t, f.e, domainUnitStage(t, f.e, f.op, "CREATE DOMAIN public.dtp_ownerless AS integer"))
	state := catalog.CloneState(f.e.catalog.state)
	domain := *domainUnitType(t, f.e.catalog.read(state), "dtp_ownerless")
	domain.Owner = authorization.PrincipalRef{}
	state.SetDescriptor(domain.Address(), &domain)
	badCatalog := f.e.catalog.read(state)
	if rows, err := dtpUnitCapture(context.Background(), badCatalog, dtpUnitAuthority(t, f, badCatalog)); rows != nil || err == nil {
		t.Fatal("ownerless user domain disclosed", rows, err)
	}
}

func TestDataTypePrivilegesVisibility(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.state)
	typ, _ := (f.e.catalog.read(state)).LookupType(sqltype.Int4)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20110}, ObjectNamespace: f.schema, ObjectName: "dtp_unit", Owner: f.owner, ObjectRevision: 1, RelationKind: catalog.TableRelation, ACL: authorization.ACLValue{Explicit: true}, Attributes: []int32{1, 3, 4}, Columns: []ColumnDescriptor{{Name: "a", Usage: typ.Usage()}, {Name: "c", Usage: typ.Usage()}, {Name: "d", Usage: typ.Usage()}}, ColumnACL: map[int32]authorization.ACLValue{3: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}}
	state.SetDescriptor(relation.Address(), relation)
	view := f.e.catalog.read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.state.CurrentUser = f.reader
	held, err := dtpUnitCapture(context.Background(), view, auth)
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
	changed := catalog.CloneState(state)
	revoked := *relation
	revoked.ColumnACL = nil
	changed.SetDescriptor(relation.Address(), &revoked)
	auth.recaptureCatalog(f.e.catalog.read(changed))
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
