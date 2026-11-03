package enginetests_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

type statioUserDemand struct {
	pgcatalog.Runtime
	fetched map[ids.ObjectAddress]int
}

func (r *statioUserDemand) IndexBlockStatistics(address ids.ObjectAddress) pgcatalog.IndexBlockFact {
	r.fetched[address]++
	return r.Runtime.IndexBlockStatistics(address)
}

func TestPGStatioUserIndexesClassification(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.reader)
	state := catalog.CloneState(f.e.Catalog.State)
	base := f.e.Catalog.Read(state)
	source := base.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioUserIndexesOID})
	if source.Address().ObjectID != 12359 || source.Name() != "pg_statio_user_indexes" || source.Namespace().ObjectID != ids.PGCatalogNamespaceOID || source.RowType != 12211 || pgcatalog.ClassKind(base, source) != "v" || source.History.HasIndexes {
		t.Fatal("selected installed identity", source)
	}
	row := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12211})
	array := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12210})
	owned, ok := base.LookupType(12211)
	ownedArray, arrayOK := base.LookupArray(owned)
	if !ok || !arrayOK || row.SQLType != owned || array.SQLType != ownedArray || row.Array != 12210 || array.SQLType.Element() != row.SQLType || row.OwningRelation != source.Address() || array.OwningRelation != source.Address() || row.Owner != source.Owner || array.Owner != source.Owner {
		t.Fatal("reciprocal owned row/array", row, array)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege || acl.Entries[0].Grantor != source.Owner {
		t.Fatal("PUBLIC SELECT", acl)
	}
	if got := pgcatalog.KeyColumns(pgcatalog.StatioUserIndexesOID); !reflect.DeepEqual(got, []int{0, 1, 3, 4}) {
		t.Fatal("advertised keys", got)
	}
	for i, c := range source.Columns {
		collation := uint32(0)
		if i >= 2 && i <= 4 {
			collation = 950
		}
		if !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.OriginTable != 12359 || c.OriginAttribute != int16(i+1) || source.ColumnMeta.CatalogAttributes[i].NotNull {
			t.Fatal("exact source metadata", i, c)
		}
	}
	cases := []struct {
		schema string
		system bool
	}{
		{"pg_catalog", true}, {"information_schema", true}, {"pg_toast", true}, {"pg_toast_temp_7", true}, {"pg_toasty", true},
		{"PG_TOAST", false}, {"Pg_Catalog", false}, {"information_schema_extra", false}, {"public", false}, {"custom", false}, {"pg_temp_7", false},
	}
	namespaces := map[string]ids.ObjectAddress{}
	for _, d := range base.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok {
			namespaces[ns.Name()] = ns.Address()
		}
	}
	parents := make([]*catalog.Relation, len(cases))
	indexes := make([]*catalog.Relation, len(cases))
	var writer statistics.Writer
	for i, tc := range cases {
		ns, ok := namespaces[tc.schema]
		if !ok {
			ns = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(35000 + i)}
			d := &catalog.Namespace{ObjectAddress: ns, ObjectName: tc.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
			state.SetDescriptor(ns, d)
			state.BindName(catalog.DescriptorNameKey(d), ns)
			namespaces[tc.schema] = ns
		}
		parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(35100 + i*2)}, ObjectNamespace: ns, ObjectName: fmt.Sprintf("user_index_parent_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation}
		// Deliberately use a different index namespace. Released classification
		// is by parent namespace, never the index's name, namespace or owner.
		index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: parent.Address().ObjectID + 1}, ObjectNamespace: f.schema, ObjectName: fmt.Sprintf("user_index_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1}}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
		state.SetDescriptor(parent.Address(), parent)
		state.SetDescriptor(index.Address(), index)
		parents[i], indexes[i] = parent, index
		// Independent selected facts differ from generic block accounting.
		fact := statistics.Fact{IndexBlocksRead: 2, IndexBlocksHit: 7, BlocksFetched: 19, BlocksHit: 13}
		if tc.system {
			fact.IndexBlocksRead, fact.IndexBlocksHit = 3, 11
		}
		if tc.schema != "custom" {
			writer.Add(&f.e.Statistics, statistics.Key{Object: f.e.Catalog.Domain.Ref(base.Epoch(), index.Address()), Category: statistics.Relation}, fact)
		}
	}
	// Exercise the inherited called parent-kind filter with represented SQLIndex
	// descriptors, without manufacturing TOAST storage or a fake SQL SYS view.
	for i, kind := range []catalog.RelationKind{catalog.MaterializedViewRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.SequenceRelation} {
		parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(35400 + i*2)}, ObjectNamespace: f.schema, ObjectName: fmt.Sprintf("kind_parent_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: kind}
		index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: parent.Address().ObjectID + 1}, ObjectNamespace: f.schema, ObjectName: fmt.Sprintf("kind_index_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address()}}
		state.SetDescriptor(parent.Address(), parent)
		state.SetDescriptor(index.Address(), index)
	}
	writer.Flush(&f.e.Statistics, time.Now(), true)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	denied := indexes[5]
	if err := auth.Check(access.Requirement{Object: f.e.Catalog.Domain.Ref(view.Epoch(), denied.Address()), Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("fixture index ACL unexpectedly admits reader")
	}
	if err := auth.CanUse(f.e.Catalog.Domain.Ref(view.Epoch(), parents[5].Namespace())); err == nil {
		t.Fatal("fixture parent namespace unexpectedly admits reader")
	}
	runtime := &statioUserDemand{Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: f.e, Auth: auth}), fetched: make(map[ids.ObjectAddress]int)}
	capture := func(oid ids.OID) map[ids.OID][]sqltype.Value {
		s := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		produce, ok := pgcatalog.ProducerFor(oid)
		if !ok {
			t.Fatal("missing producer", oid)
		}
		rows, err := produce(pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), s.Address()), Source: s, Runtime: runtime})
		if err != nil {
			t.Fatal(err)
		}
		bag := map[ids.OID][]sqltype.Value{}
		for _, r := range rows {
			n, err := r[1].OID()
			if err != nil {
				t.Fatal(err)
			}
			if bag[ids.OID(n)] != nil {
				t.Fatal("duplicate identity", n)
			}
			bag[ids.OID(n)] = r
		}
		return bag
	}
	all := capture(pgcatalog.StatioAllIndexesOID)
	clear(runtime.fetched)
	user := capture(pgcatalog.StatioUserIndexesOID)
	if len(runtime.fetched) != len(user) {
		t.Fatal("USER demanded nonmember provider facts", runtime.fetched, user)
	}
	activity := capture(pgcatalog.StatAllIndexesOID)
	for i, tc := range cases {
		r := all[indexes[i].Address().ObjectID]
		if len(r) != 7 {
			t.Fatal("missing actual ALL identity", tc, r)
		}
		parentID, _ := r[0].OID()
		schema, _ := r[2].Text()
		parentName, _ := r[3].Text()
		indexName, _ := r[4].Text()
		read, _ := r[5].Integer()
		hit, _ := r[6].Integer()
		wantRead, wantHit := int64(2), int64(7)
		if tc.system {
			wantRead, wantHit = 3, 11
		}
		if tc.schema == "custom" {
			wantRead, wantHit = 0, 0
		}
		if parentID != uint32(parents[i].Address().ObjectID) || schema != tc.schema || parentName != parents[i].Name() || indexName != indexes[i].Name() || read != wantRead || hit != wantHit || r[5].IsNull() || r[6].IsNull() {
			t.Fatal("independent identities/selected facts", tc, r)
		}
		selected, present := user[indexes[i].Address().ObjectID]
		if present == tc.system || (present && !reflect.DeepEqual(selected, r)) {
			t.Fatal("exact parent-schema membership/shared projection", tc, selected, r)
		}
		wantDemand := 1
		if tc.system {
			wantDemand = 0
		}
		if got := runtime.fetched[indexes[i].Address()]; got != wantDemand {
			t.Fatal("parent-schema exclusion must precede Reader", tc, got, wantDemand)
		}
		// These block facts do not authorize fabricating last-scan activity in
		// the separate pg_stat_all_indexes subsystem, including missing facts.
		a := activity[indexes[i].Address().ObjectID]
		if len(a) != 9 || !a[6].IsNull() {
			t.Fatal("unrelated absent activity lost SQL NULL", tc, a)
		}
	}
	if len(all) != len(cases)+1 || len(user) != 7 {
		t.Fatal("called parent kind filter/category cardinality", len(all), len(user))
	}
}
