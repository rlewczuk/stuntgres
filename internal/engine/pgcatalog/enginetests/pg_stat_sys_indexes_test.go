package enginetests_test

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

type sysIndexDemand struct {
	pgcatalog.Runtime
	fetched map[ids.ObjectAddress]int
}

func (r *sysIndexDemand) IndexStatistics(address ids.ObjectAddress) pgcatalog.IndexStatisticsFact {
	r.fetched[address]++
	return r.Runtime.IndexStatistics(address)
}

func TestPGStatSysIndexesMembership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	base := f.e.Catalog.Read(state)
	cases := []struct {
		schema string
		system bool
	}{
		{"pg_catalog", true}, {"information_schema", true}, {"pg_toast", true},
		{"pg_toast_extra", true}, {"pg_toast_temp_7", true}, {"pg_toasty", true},
		{"public", false}, {"pg_temp_7", false}, {"PG_TOAST", false},
		{"xpg_toast", false}, {"pg_catalog_extra", false}, {"information_schema_extra", false},
	}
	namespaces := map[string]ids.ObjectAddress{}
	for _, d := range base.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok {
			namespaces[ns.Name()] = ns.Address()
		}
	}
	expected := map[ids.ObjectAddress]bool{}
	parents := map[ids.ObjectAddress]*catalog.Relation{}
	add := func(number int, namespace ids.ObjectAddress, kind catalog.RelationKind, admitted bool) {
		parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(36100 + number*2)}, ObjectNamespace: namespace, ObjectName: fmt.Sprintf("six_parent_%d", number), ObjectRevision: 1, Owner: f.owner, RelationKind: kind}
		// Index labels and namespace deliberately contradict classification. These
		// are staged catalog fixtures, not physical TOAST storage or SQL DDL.
		index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: parent.Address().ObjectID + 1}, ObjectNamespace: f.schema, ObjectName: fmt.Sprintf("pg_catalog_misleading_%d", number), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Ready: false, Valid: false}}
		state.SetDescriptor(parent.Address(), parent)
		state.SetDescriptor(index.Address(), index)
		expected[index.Address()] = admitted
		parents[index.Address()] = parent
	}
	for i, tc := range cases {
		ns, ok := namespaces[tc.schema]
		if !ok {
			ns = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(36000 + i)}
			d := &catalog.Namespace{ObjectAddress: ns, ObjectName: tc.schema, ObjectRevision: 1, Owner: f.owner}
			state.SetDescriptor(ns, d)
			state.BindName(catalog.DescriptorNameKey(d), ns)
			namespaces[tc.schema] = ns
		}
		add(i, ns, catalog.TableRelation, tc.system)
	}
	// ClassKind has no represented TOAST relation kind. Preserve the producer's
	// release r/t/m condition; test every representable parent kind here.
	for i, kind := range []catalog.RelationKind{catalog.MaterializedViewRelation, catalog.PartitionedRelation, catalog.ForeignRelation, catalog.ViewRelation, catalog.SequenceRelation} {
		add(len(cases)+i, namespaces["information_schema"], kind, kind == catalog.MaterializedViewRelation)
	}
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	runtime := &sysIndexDemand{Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: f.e, Auth: auth}), fetched: make(map[ids.ObjectAddress]int)}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSysIndexesOID}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatSysIndexesOID)
	if !ok {
		t.Fatal("missing SYS producer")
	}
	rows, err := produce(pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), address), Source: view.MustRelation(address), Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[ids.ObjectAddress]bool{}
	for _, row := range rows {
		n, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		index := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(n)}
		if !expected[index] || seen[index] {
			t.Fatal("unexpected/duplicate SYS identity", row)
		}
		seen[index] = true
		parent := parents[index]
		parentID, _ := row[0].OID()
		namespace, _ := view.Namespace(parent.Namespace())
		schema, _ := row[2].Text()
		name, _ := row[3].Text()
		if parentID != uint32(parent.Address().ObjectID) || schema != namespace.Name() || name != parent.Name() || !row[6].IsNull() {
			t.Fatal("parent identity/missing time", row)
		}
		for _, column := range []int{5, 7, 8} {
			if value, err := row[column].Integer(); err != nil || value != 0 {
				t.Fatal("missing activity must be typed zero", row, err)
			}
		}
	}
	for address, admitted := range expected {
		wantDemand := 0
		if admitted {
			wantDemand = 1
		}
		if seen[address] != admitted || runtime.fetched[address] != wantDemand {
			t.Fatal("membership/kind rejection must precede Reader", address, admitted, seen[address], runtime.fetched[address])
		}
	}
	if len(runtime.fetched) != len(rows) {
		t.Fatal("nonmember demanded shared facts", runtime.fetched, rows)
	}
}
