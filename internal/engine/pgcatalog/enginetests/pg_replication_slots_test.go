package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGReplicationSlotsProducer(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ReplicationSlotsOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing slot inventory source")
	}
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	acl := source.Security.ACL
	if source.Owner != owner || !acl.Explicit || len(acl.Entries) != 2 ||
		acl.Entries[0] != (authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges}) ||
		acl.Entries[1] != (authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}) {
		t.Fatal("owner-first full-owner and PUBLIC SELECT seed", source.Owner, acl)
	}
	row := view.State().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12263})
	array := view.State().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12262})
	ownedArray, arrayOK := view.LookupArray(row.SQLType)
	if source.RowType != 12263 || row.Array != 12262 || !arrayOK || ownedArray != array.SQLType || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address {
		t.Fatal("slot nominal type links", source, row, array)
	}
	names := []string{"slot_name", "plugin", "slot_type", "datoid", "database", "temporary", "active", "active_pid", "xmin", "catalog_xmin", "restart_lsn", "confirmed_flush_lsn", "wal_status", "safe_wal_size", "two_phase", "two_phase_at", "inactive_since", "conflicting", "invalidation_reason", "failover", "synced"}
	types := []sqltype.ID{19, 19, 25, 26, 19, 16, 16, 23, 28, 28, 3220, 3220, 25, 20, 16, 3220, 1184, 16, 25, 16, 16}
	if len(source.Columns) != len(names) {
		t.Fatal(source.Columns)
	}
	for i, c := range source.Columns {
		typ, ok := view.LookupType(types[i])
		collation := uint32(0)
		if types[i] == sqltype.Name {
			collation = 950
		} else if types[i] == sqltype.Text {
			collation = 100
		}
		if !ok || c.Name != names[i] || c.Usage.Type != typ || c.Usage.Collation.ID != collation || c.Usage.Typmod != -1 || c.DeclaredDimensions != 0 || !c.Nullable || !c.NullabilityKnown {
			t.Fatal("slot attribute", i, c)
		}
	}
	produce, ok := pgcatalog.ProducerFor(address.ObjectID)
	if !ok {
		t.Fatal("missing inventory producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	rows, err := produce(in)
	if err != nil || rows != nil {
		t.Fatal("empty inventory must allocate no row", rows, err)
	}
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil || captured == nil || len(captured.Anonymous) != 0 {
		t.Fatal(captured, err)
	}
	reject := func(value pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(value); rows != nil || err == nil {
			t.Fatal("invalid authority published", rows, err)
		}
	}
	for _, change := range []func(*pgcatalog.Input){
		func(v *pgcatalog.Input) { v.Context = nil },
		func(v *pgcatalog.Input) { v.Owner = nil },
		func(v *pgcatalog.Input) { v.Owner = foreign.CatalogRead().Owner() },
		func(v *pgcatalog.Input) { v.Catalog = catalog.Read{} },
		func(v *pgcatalog.Input) { v.Catalog = foreign.CatalogRead() },
		func(v *pgcatalog.Input) { v.Ref = ids.ObjectRef{} },
		func(v *pgcatalog.Input) { v.Ref.Epoch++ },
		func(v *pgcatalog.Input) { v.Ref.Address.ObjectID++ },
		func(v *pgcatalog.Input) { v.Ref = foreign.Catalog.Domain.Ref(view.Epoch(), address) },
		func(v *pgcatalog.Input) { v.Source = nil },
		func(v *pgcatalog.Input) { copied := *source; v.Source = &copied },
		func(v *pgcatalog.Input) { v.Roles = authorization.Read{} },
		func(v *pgcatalog.Input) { v.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}) },
	} {
		value := in
		change(&value)
		reject(value)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:20] },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Int4) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Name) },
		func(r *catalog.Relation) { r.Columns[0].Nullable = false },
		func(r *catalog.Relation) { r.Columns[0].NullabilityKnown = false },
		func(r *catalog.Relation) { r.Columns[0].Usage.Collation.ID = 100 },
		func(r *catalog.Relation) { r.Columns[2].Usage.Collation.ID = 950 },
		func(r *catalog.Relation) { r.Columns[3].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[4].DeclaredDimensions = 1 },
	} {
		state := catalog.CloneState(view.State())
		copySource := *source
		copySource.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&copySource)
		state.SetDescriptor(address, &copySource)
		value := in
		value.Catalog, value.Source = e.Catalog.Read(state), &copySource
		reject(value)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{ctx, &subscriptionCaptureCancellation{Context: t.Context(), remaining: 1}} {
		value := in
		value.Context = ctx
		if rows, err := produce(value); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(rows, err)
		}
	}
}
