package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGReplicationOriginStatusProducer(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	state := catalog.CloneState(e.CatalogRead().State())
	for id, name := range map[ids.OID]string{41: "nameA", 42: "nameB"} {
		d := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: id}, ObjectRevision: 1, ObjectName: name}
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.ReplicationOriginNameKey(name), d.Address())
	}
	view := e.Catalog.Read(state)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ReplicationOriginStatusOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing status source")
	}
	for i, column := range source.Columns {
		collation := uint32(0)
		if i == 1 {
			collation = 100
		}
		if column.Usage.Collation.ID != collation {
			t.Fatal("released status collation", i, column.Usage.Collation.ID)
		}
	}
	produce, ok := pgcatalog.ProducerFor(address.ObjectID)
	if !ok {
		t.Fatal("missing producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	for _, progress := range []map[ids.OID]uint64{nil, {}, {41: 0}, {41: ^uint64(0), 43: 0}} {
		in.ReplicationOriginProgress = progress
		before := make(map[ids.OID]uint64, len(progress))
		for k, v := range progress {
			before[k] = v
		}
		captured, err := catalogrt.CaptureRows(in, produce)
		if err != nil || captured == nil || len(captured.Anonymous) != len(progress) {
			t.Fatal(captured, err)
		}
		seen := map[uint32]bool{}
		for _, row := range captured.Anonymous {
			if len(row) != 4 {
				t.Fatal(row)
			}
			for i, value := range row {
				if value.Type() != source.Columns[i].Usage.Type {
					t.Fatal("foreign datum", i)
				}
			}
			id, err := row[0].OID()
			if err != nil || seen[id] {
				t.Fatal(id, err)
			}
			seen[id] = true
			remote, err := row[2].PGLSN()
			if err != nil || remote != progress[ids.OID(id)] {
				t.Fatal(remote, err)
			}
			local, err := row[3].PGLSN()
			if err != nil || local != 0 {
				t.Fatal(local, err)
			}
			if id == 43 {
				if !row[1].IsNull() {
					t.Fatal("orphan fabricated name")
				}
			} else {
				text, err := row[1].Type().Output(t.Context(), row[1])
				if err != nil || text != "nameA" {
					t.Fatal(text, err)
				}
			}
		}
		for id, value := range before {
			if progress[id] != value {
				t.Fatal("producer mutated input")
			}
		}
		if len(progress) != len(before) || view.State() != state || view.NameAddress(catalog.ReplicationOriginNameKey("nameB")).ObjectID != 42 {
			t.Fatal("producer mutated authority")
		}
	}
	reject := func(value pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(value); rows != nil || err == nil {
			t.Fatal("invalid capture published", rows, err)
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
		func(v *pgcatalog.Input) {
			v.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
	} {
		value := in
		change(&value)
		reject(value)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:3] },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Int4) },
		func(r *catalog.Relation) { r.Columns[1].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Text) },
		func(r *catalog.Relation) { r.Columns[0].Nullable = false },
		func(r *catalog.Relation) { r.Columns[0].NullabilityKnown = false },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 0 },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 950 },
		func(r *catalog.Relation) { r.Columns[2].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[3].DeclaredDimensions = 1 },
	} {
		copyState := catalog.CloneState(state)
		copySource := *source
		copySource.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&copySource)
		copyState.SetDescriptor(address, &copySource)
		value := in
		value.Catalog, value.Source = e.Catalog.Read(copyState), &copySource
		reject(value)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{ctx, &subscriptionCaptureCancellation{Context: t.Context(), remaining: 2}} {
		value := in
		value.Context = ctx
		if rows, err := produce(value); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(rows, err)
		}
	}
	if !reflect.DeepEqual(in.ReplicationOriginProgress, map[ids.OID]uint64{41: ^uint64(0), 43: 0}) {
		t.Fatal("input changed")
	}
}
